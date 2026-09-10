// frame.go answers, for one backbone event, which task or document the
// Progress page has to refresh (WL-SPEC-66 §5.1). Pure, like the rest of
// this package: the event type and its recorded payload, nothing else.
package progress

import (
	"encoding/json"
	"strings"
)

// Touch is what one backbone event names: at most one task and one
// document, by id. Zero values mean the event touches nothing this page
// shows.
type Touch struct {
	Task string
	// Tasks is the set a delivery event transitioned, recorded on the event
	// by the resolver's callers because the transitions log no event of their
	// own. Task stays for the events that name exactly one task, which is
	// most of them; a producer sets one field or the other, not both.
	Tasks []string
	Doc   int64
	// DocIRI is the document's subject IRI (wlid:doc/spec-worklode-066),
	// set when the event names its document that way and not by row id.
	// The reader resolves it to a row id; this package stays pure.
	DocIRI string
	Kind   string // "task" | "doc" | "pr" | "ci" | "deploy" | "rally" | ""
}

// touchKinds maps an event type's family — the segment before its first dot,
// which is how every producer in the backbone names its events — to the kind
// of thing a frame about it is. Families rather than whole types because the
// task family alone has two dozen members (task.created, task.done,
// task.deployed_prod, ...) and a member added later must refresh the page
// rather than be silently dropped.
//
// A family absent here touches nothing this page shows: crew, project,
// milestone, approval, approval_flow, deliverable, runtime, catalog, inbox,
// merge, secrets_materialized, and the GitHub issues, pull_request_review,
// release and registry_package deliveries.
var touchKinds = map[string]string{
	// internal/api: lifecycle, assign, tasks, hierarchy, checklist,
	// softdelete, deleted, progress, docwatch; internal/store: instructions,
	// decisions.
	"task": "task",
	// internal/store: leases, agent_sessions, decisions.
	"lease":         "task",
	"agent_session": "task",
	"decision":      "task",
	// internal/api/admin.go: issue.promoted, issue.linked, issue.dismissed.
	"issue": "task",
	// internal/api/docs.go via recordDocEvent, plus the two typed events
	// internal/eventbus emits (025 §15.3), which carry no dot.
	"doc":                  "doc",
	"wl:DocumentSubmitted": "doc",
	"wl:DocumentAccepted":  "doc",
	// internal/api/progress.go.
	"rally": "rally",
	// internal/hooks: the GitHub and Flux deliveries the page reacts to.
	"pull_request":      "pr",
	"merge_group":       "pr",
	"workflow_run":      "ci",
	"push":              "deploy",
	"deployment_status": "deploy",
	"flux":              "deploy",
}

// Resolve returns the ids one event names. eventType is events.type and
// payload is the event's recorded payload. A type whose family is not in
// touchKinds resolves to the zero Touch; a type that is resolves to its kind
// even when the payload names no id, which is still a signal the page's
// summary is stale (§5.2 refreshes it on any frame).
func Resolve(eventType string, payload []byte) Touch {
	family, _, _ := strings.Cut(eventType, ".")
	kind, ok := touchKinds[family]
	if !ok {
		return Touch{}
	}
	t := Touch{Kind: kind}
	// Decoded key by key: an event this page does not model is free to use
	// "task" or "doc" for something else, and one bad key must not cost the
	// other one.
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return t
	}
	_ = json.Unmarshal(fields["task"], &t.Task)
	_ = json.Unmarshal(fields["tasks"], &t.Tasks)
	// "doc" is the row id in a document mutation's payload and the document's
	// IRI in the events minted about one (api/progress.go, api/docwatch.go);
	// the typed 025 §15.3 events name their document in "wl:subject" and
	// nowhere else. Whichever form is there, the document is named — the row
	// id directly, the IRI for the reader to resolve (WL-SPEC-66 §5.1).
	if json.Unmarshal(fields["doc"], &t.Doc) != nil {
		_ = json.Unmarshal(fields["doc"], &t.DocIRI)
	}
	if t.Doc == 0 && t.DocIRI == "" {
		_ = json.Unmarshal(fields["wl:subject"], &t.DocIRI)
	}
	return t
}
