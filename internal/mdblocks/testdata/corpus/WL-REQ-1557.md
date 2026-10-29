
`LODE_WEB_OPEN` is never set in the prod overlay or on any other reachable instance. The web routes refuse to serve with no provider configured, and that opt-in is the only way around it. Placement never substitutes for the session gate (WL-SPEC-82).

