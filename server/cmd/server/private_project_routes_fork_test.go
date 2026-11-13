package main

// What this fork changes in the registry of private_project_routes_test.go:
// the parameters of the routes it adds, and of the upstream routes it does
// not register. Applying them from here leaves that file as upstream has it.
func init() {
	for _, key := range []string{
		"checkout-sessions/{sessionId}",
		"groups/{conversationId}",
		"logins/{loginId}",
	} {
		delete(routeParamsOutsideProjects, key)
	}
}
