// Package eventbus implements WL-SPEC-77 §15: typed domain-event emission and
// the offset-tracked subscriber loop over the store's events table.
package eventbus

// Hand-mirrored from ns/ontology.ttl (WL-SPEC-77 §15).
// TODO(WL-SPEC-77 §14): fold into scripts/nsgen.py output when the codegen lands.
// vocab_test.go holds the mirror together.
const (
	TypeDocumentSubmitted = "wl:DocumentSubmitted"
	TypeDocumentAccepted  = "wl:DocumentAccepted"
)

// baseProperties are allowed in every domain-event payload.
var baseProperties = []string{
	"@context", "@type", "@id", "prov:atTime", "prov:wasAssociatedWith", "wl:subject",
}

// payloadProperties maps each event type to its additional allowed payload
// properties. Emit-time validation (emit.go) enforces membership; there is
// deliberately no CHECK on events.type — the log also holds vendor webhook
// deliveries with dotted types (WL-SPEC-77 §15).
var payloadProperties = map[string][]string{
	TypeDocumentSubmitted: {},
	TypeDocumentAccepted:  {"wl:fromStatus", "wl:toStatus"},
}

// KnownType reports whether typ is a generated domain-event type. Metrics
// use it to bound the type label (§8: unknown counts as "other").
func KnownType(typ string) bool { _, ok := payloadProperties[typ]; return ok }
