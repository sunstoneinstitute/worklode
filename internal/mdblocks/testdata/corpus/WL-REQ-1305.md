
`internal/api` holds the list of path patterns the [SPA](/projects/worklode/term/spa) handles, in the same
syntax as `src/router.tsx`. A test fails when the two disagree, so a page
added to the SPA and not to the list (or the reverse) breaks the build.

Porting a page is one change: the SPA route, its entry in the list, its API
endpoints, and the deletion of the templ route it replaces (WL-REQ-1303).
A link from an SPA page to a path not in the list is a full page load, so
the templ cockpit serves it.
