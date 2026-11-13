package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
)

// This file is what makes the server a private deployment: one that runs
// inside an organization, reports nothing outside it, and offers Feishu as its
// only messaging channel. main applies it; the router, the handlers and their
// tests are the product as built.

// deploymentEnvDefaults are set when the operator has not set them. Telemetry
// to Multica's collector and product analytics are off unless the operator
// turns them on by setting the variable to 0 or false.
var deploymentEnvDefaults = map[string]string{
	"DO_NOT_TRACK":       "1",
	"ANALYTICS_DISABLED": "true",
}

func applyDeploymentEnvDefaults() {
	for key, value := range deploymentEnvDefaults {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			os.Setenv(key, value)
		}
	}
}

// Routes this deployment does not serve. They stay registered, so the route
// table is upstream's; a request for one is answered as if it were not there.
// A route is matched by its pattern rather than by the request path, so a
// workspace or resource that happens to be named "slack" is not caught, and a
// route upstream adds to one of these families is closed without a change
// here.
var (
	// Whole path segments of a pattern: the messaging channels other than
	// Feishu, and runtimes hosted by a vendor.
	closedRouteSegments = []string{"slack", "dingtalk", "wecom", "weixin", "telegram", "qoder"}
	// Patterns at or under these: the hosted product's billing, its runtime
	// fleet, and its sales form.
	closedRoutePrefixes = []string{
		"/api/cloud-billing",
		"/api/cloud-subscriptions",
		"/api/cloud-runtime",
		"/api/webhooks/stripe",
		"/api/contact-sales",
	}
)

func routeIsClosed(pattern string) bool {
	if pattern == "" {
		return false
	}
	for _, prefix := range closedRoutePrefixes {
		if pattern == prefix || strings.HasPrefix(pattern, prefix+"/") {
			return true
		}
	}
	for _, segment := range strings.Split(pattern, "/") {
		for _, closed := range closedRouteSegments {
			if segment == closed {
				return true
			}
		}
	}
	return false
}

// closeDeploymentRoutes serves routes, answering 404 for the closed ones.
func closeDeploymentRoutes(routes chi.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		if routeIsClosed(routes.Find(chi.NewRouteContext(), r.Method, path)) {
			http.NotFound(w, r)
			return
		}
		routes.ServeHTTP(w, r)
	})
}
