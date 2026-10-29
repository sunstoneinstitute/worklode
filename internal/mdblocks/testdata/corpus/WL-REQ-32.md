
The server carries a private Prometheus registry with the Go and process collectors, `http_requests_total` and `http_request_duration_seconds` middleware with mux-pattern route labels, and `/metrics` on the admin listener (port 9090). Nothing lands on the public mux.

Rules for every metric:

- `lode-server`'s main package is the composition root. It creates the registry and passes a `prometheus.Registerer` into each subsystem.
- `api.NewServer` takes the registry, registers the HTTP middleware metrics, and serves `promhttp.HandlerFor` with `ContinueOnError` and an `ErrorLog`. A failing collector drops its family and surfaces as `promhttp_metric_handler_errors_total{cause="gathering"}`. With no registry passed it falls back to a private one.
- Every other subsystem owns its instruments in a package-private `metrics.go` next to the code it measures. No central metrics package.
- Every metrics struct is nil-safe. A nil receiver no-ops, so the CLI and tests pay nothing.
- Every metric is prefixed `worklode_`, except the `http_requests_total` and `http_request_duration_seconds` pair.
- Label values are bounded. `outcome` names a domain result from the store's sentinel errors. `result` names plain success or failure. Do not mix them.
- A cancelled context records nothing. A deadline exceeded counts as `error`.
- Server-side changes adding an endpoint, background loop, outbound call or store operation with meaningful outcomes add or extend `worklode_*` metrics in the owning package, with `prometheus/testutil` assertions and the `TestMetricsEndpointDomainFamilies` family check.

