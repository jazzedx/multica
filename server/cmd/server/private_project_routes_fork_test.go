package main

// What this fork changes in the registry of private_project_routes_test.go:
// the parameters of the routes it adds, and of the upstream routes it does
// not register. Applying them from here leaves that file as upstream has it.
func init() {
	for key, param := range map[string]outsideProjectParam{
		"download/{goos}":   {why: "the operating system of a CLI build"},
		"download/{goarch}": {why: "the architecture of a CLI build"},
	} {
		routeParamsOutsideProjects[key] = param
	}
	for _, key := range []string{
		"project-folders/{id}",
	} {
		delete(routeParamsOutsideProjects, key)
	}
}
