package ns

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
