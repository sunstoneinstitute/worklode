package ns

// This file is hand-written, unlike gen.go beside it. A retirement could be
// expressed in ns/concept.ttl as a skos:hiddenLabel on the current term, but
// deliberately isn't: an alias is a transient input shim for callers still
// using an old spelling, not an ontology fact worth recording permanently.
// The next retirement needs its own entry here — it will not show up in the
// TTL and scripts/nsgen.py will not generate one.

// DeprecatedTaskKinds maps a retired task-kind spelling to the kind it became
// (025 §10 renamed spec → design, migration 0025). It is input-only: callers
// normalise before validating, and only the current name is ever persisted, so
// the tasks table never carries two spellings of one kind.
var DeprecatedTaskKinds = map[string]string{"spec": "design"}

// NormalizeTaskKind rewrites a deprecated spelling to its current name,
// reporting whether an alias was used. Anything else passes through untouched
// for the caller's own validity gate to accept or reject.
func NormalizeTaskKind(kind string) (string, bool) {
	if current, ok := DeprecatedTaskKinds[kind]; ok {
		return current, true
	}
	return kind, false
}
