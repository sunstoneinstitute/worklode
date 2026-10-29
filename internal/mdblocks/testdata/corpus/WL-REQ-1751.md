
Refetch is conditional on the object's monotonically increasing `version`, returned by `GET /api/v1/docs/{id}`. The client sends the version it holds; the server answers with the body when it moved and not-modified when it did not. The mechanism is defined on the version field in `internal/model`, so task and plan bodies use the same path. `worklode_doc_fetch_total{result}` counts `modified`, `not_modified`, `not_found`. With no backbone reachable, a stale cache is used rather than discarded.

