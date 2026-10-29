
The new web UI is a React single-page app (the [SPA](/projects/worklode/term/spa)) built from the
`worklode-design` prototype. It replaces the [templ cockpit](/projects/worklode/term/templ-cockpit) (WL-REQ-334)
page by page. During the migration both run from the same `lode-server`
binary on one host, `LODE_PUBLIC_URL`, and each path is served by exactly
one of them (WL-REQ-1303).

The SPA has no server of its own. There is no server-side rendering, no
backend-for-frontend and no GraphQL layer (WL-RULE-1312).
