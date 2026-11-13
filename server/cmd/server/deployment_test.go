package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestDeploymentEnvDefaultsRespectTheOperator(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("ANALYTICS_DISABLED", "false")

	applyDeploymentEnvDefaults()

	if got := os.Getenv("DO_NOT_TRACK"); got != "1" {
		t.Errorf("DO_NOT_TRACK = %q, want the default 1", got)
	}
	if got := os.Getenv("ANALYTICS_DISABLED"); got != "false" {
		t.Errorf("ANALYTICS_DISABLED = %q, want the operator's false", got)
	}
}

func TestCloseDeploymentRoutes(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	r := chi.NewRouter()
	r.Post("/api/contact-sales", ok)
	r.Post("/api/slack/binding/redeem", ok)
	r.Route("/api/cloud-billing", func(r chi.Router) {
		r.Get("/balance", ok)
	})
	r.Route("/api/workspaces/{id}", func(r chi.Router) {
		r.Get("/", ok)
		r.Group(func(r chi.Router) {
			r.Get("/lark/installations", ok)
			r.Get("/telegram/installations", ok)
			r.Put("/qoder", ok)
		})
	})
	r.Get("/api/skills/{name}", ok)
	handler := closeDeploymentRoutes(r)

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/contact-sales", http.StatusNotFound},
		{http.MethodPost, "/api/slack/binding/redeem", http.StatusNotFound},
		{http.MethodGet, "/api/cloud-billing/balance", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/w1/telegram/installations", http.StatusNotFound},
		{http.MethodPut, "/api/workspaces/w1/qoder", http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/w1/lark/installations", http.StatusNoContent},
		// A closed name as a parameter value is someone's own resource.
		{http.MethodGet, "/api/workspaces/slack/", http.StatusNoContent},
		{http.MethodGet, "/api/skills/telegram", http.StatusNoContent},
		// Not a route at all: the router's own 404.
		{http.MethodGet, "/api/nothing", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
