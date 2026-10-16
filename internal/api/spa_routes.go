package api

import "strings"

// spaRoutes is every path pattern the SPA handles (WL-REQ-1305), in
// TanStack Router's syntax. web/app/src/routes.json carries the same list;
// TestSPARoutesMatchRouter fails when the two disagree. registerSPA routes
// each one to the SPA shell; the templ cockpit serves every other path.
var spaRoutes = []string{
	"/projects",
	"/projects/$projectId",
	"/projects/$projectId/crew",
	"/projects/$projectId/documents",
	"/projects/$projectId/tasks",
	"/tasks",
}

// muxPattern turns a TanStack route ("/projects/$id") into its ServeMux path
// ("/projects/{id}"), which is also its routeGuards key.
func muxPattern(route string) string {
	segs := strings.Split(route, "/")
	for i, seg := range segs {
		if name, ok := strings.CutPrefix(seg, "$"); ok {
			segs[i] = "{" + name + "}"
		}
	}
	return strings.Join(segs, "/")
}
