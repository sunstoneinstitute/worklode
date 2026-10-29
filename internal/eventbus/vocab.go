// Package eventbus implements WL-RULE-179: typed domain-event emission and
// the offset-tracked subscriber loop over the store's events table.
package eventbus

// Hand-mirrored from ns/ontology.ttl (WL-RULE-179).
// TODO(WL-REQ-178): fold into scripts/nsgen.py output when the codegen lands.
// vocab_test.go holds the mirror together.
const (
	TypeDocumentSubmitted = "wl:DocumentSubmitted"
	TypeDocumentAccepted  = "wl:DocumentAccepted"

	TypeReferenceClassified    = "wl:ReferenceClassified"
	TypeClassificationRepaired = "wl:ClassificationRepaired"

	TypeReviewOpened           = "wl:ReviewOpened"
	TypeReviewRoundSubmitted   = "wl:ReviewRoundSubmitted"
	TypeReviewQuestionAsked    = "wl:ReviewQuestionAsked"
	TypeReviewQuestionAnswered = "wl:ReviewQuestionAnswered"
	TypeReviewThreadResolved   = "wl:ReviewThreadResolved"
	TypeReviewVerdictRecorded  = "wl:ReviewVerdictRecorded"
	TypeReviewClosed           = "wl:ReviewClosed"
)

// baseProperties are allowed in every domain-event payload.
var baseProperties = []string{
	"@context", "@type", "@id", "prov:atTime", "prov:wasAssociatedWith", "wl:subject",
}

// payloadProperties maps each event type to its additional allowed payload
// properties. Emit-time validation (emit.go) enforces membership; there is
// deliberately no CHECK on events.type — the log also holds vendor webhook
// deliveries with dotted types (WL-RULE-179).
var payloadProperties = map[string][]string{
	TypeDocumentSubmitted: {},
	TypeDocumentAccepted:  {"wl:fromStatus", "wl:toStatus"},
	// The payload of these two is a ref_classifications row, not a DomainEvent.
	TypeReferenceClassified:    {},
	TypeClassificationRepaired: {},

	TypeReviewOpened:           {},
	TypeReviewRoundSubmitted:   {"wl:reviewRole", "wl:client", "wl:focused", "wl:threadCount"},
	TypeReviewQuestionAsked:    {"wl:threadIntent"},
	TypeReviewQuestionAnswered: {},
	TypeReviewThreadResolved:   {"wl:threadStatus"},
	TypeReviewVerdictRecorded:  {"wl:reviewVerdict"},
	TypeReviewClosed:           {"wl:outcome"},
}

// KnownType reports whether typ is a generated domain-event type. Metrics
// use it to bound the type label (unknown counts as "other").
func KnownType(typ string) bool { _, ok := payloadProperties[typ]; return ok }
