package api

import "strings"

// spaRoutes is every path pattern the SPA handles (WL-REQ-1305), in
// TanStack Router's syntax. web/app/src/routes.json carries the same list;
// TestSPARoutesMatchRouter fails when the two disagree. A public-host GET
// for a path not matched here redirects to LODE_LEGACY_URL.
var spaRoutes = []string{
	"/projects",
}

// spaHandles reports whether path matches one of spaRoutes: same number of
// segments, and each segment equal or a "$param" against a non-empty one.
func spaHandles(path string) bool {
	pathSegs := strings.Split(path, "/")
	for _, route := range spaRoutes {
		routeSegs := strings.Split(route, "/")
		if len(routeSegs) != len(pathSegs) {
			continue
		}
		if routeMatches(routeSegs, pathSegs) {
			return true
		}
	}
	return false
}

// routeMatches reports whether every segment of path matches the
// corresponding segment of route: equal, or route's is a "$param" against a
// non-empty path segment.
func routeMatches(routeSegs, pathSegs []string) bool {
	for i, seg := range routeSegs {
		if strings.HasPrefix(seg, "$") {
			if pathSegs[i] == "" {
				return false
			}
			continue
		}
		if seg != pathSegs[i] {
			return false
		}
	}
	return true
}
