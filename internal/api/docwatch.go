package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// docLifecycleSubscriber is the subscriber name this server consumes the log
// under (025 §15.4). It is the offset row `lode event seek` addresses, so it
// is a name in the operator's vocabulary, not an implementation detail.
const docLifecycleSubscriber = "doc-lifecycle"

// watcherActorID is tasks.created_by on everything the rules mint, so
// "who created this task" answers with the mechanism rather than with
// whoever happened to submit the document. tasks.created_by REFERENCES
// actors(id), so the row must exist before the first mint — NewServer
// asserts it with EnsureServiceActor when it starts the loop.
const watcherActorID = "watcher"

// watcherEventSource is events.source on the action events the executor
// records. Together with the external id below it forms the
// (source, external_id) unique key, which is the whole idempotency story:
//
//   - Layer 1, exactly-once per delivery: every action's external id ends in
//     the triggering event's id ("doc-lifecycle:<rule>:<event-id>"), so a
//     redelivered event collides on the constraint. RecordEvent then returns
//     the existing id and skips apply entirely — which is what makes the
//     mint happen once, with no side-effect table to keep in step (025 §15.4).
//   - Layer 2, the open-task guard: a genuinely new event (a later version,
//     hence a different id) still mints nothing while an open task of that
//     kind already references the document. That is internal/watcher's
//     decision, not this file's.
//
// Both layers are needed and neither subsumes the other: layer 1 is about the
// same event arriving twice, layer 2 about two different events meaning the
// same outstanding work.
const watcherEventSource = "watcher"

// handleDocLifecycle is the doc-lifecycle subscriber of spec 025 §15.4: it
// parses the event, fetches the guard facts, lets the pure rules decide
// (internal/watcher), and performs whatever they return. The action event's
// payload carries prov:wasInformedBy back to the triggering event, so the
// provenance chain webhook → event → task holds through the watcher.
//
// The action events are dotted types (task.created, task.updated), so
// eventbus's JSON-LD payload validation does not apply to them — they are
// backbone bookkeeping, not RDF the knowledge graph consumes.
func (s *server) handleDocLifecycle(ctx context.Context, ev store.Event) (eventbus.Outcome, error) {
	if ev.Type != eventbus.TypeDocumentSubmitted && ev.Type != eventbus.TypeDocumentAccepted {
		// The vendor/webhook population of the log passes through
		// untouched: it carries dotted types (push, pod.crashloop, …;
		// 025 §15.2) that are not RDF and that these rules say nothing
		// about. Acking them is what keeps the offset moving.
		return eventbus.OutcomeApplied, nil
	}

	// Every error path below returns OutcomeApplied alongside the error:
	// Run ignores the outcome whenever the error is non-nil (it counts
	// outcome="error" itself), and 025 §15.7 forbids a handler from
	// returning OutcomeError.
	subject, err := eventSubject(ev)
	if err != nil {
		return eventbus.OutcomeApplied, err
	}
	// A wl:subject that resolves to no row is an error, not a skip.
	// Redelivery retries it, which is right while the doc is merely
	// not visible yet; if the document is really gone, the operator
	// steps the subscriber past it with `lode event seek` — that is
	// what the verb is for.
	doc, err := s.st.DocBySubjectIRI(ctx, subject)
	if err != nil {
		return eventbus.OutcomeApplied, fmt.Errorf("doc-lifecycle: event %d: %w", ev.ID, err)
	}

	in := watcher.Input{
		EventID:   ev.ID,
		EventType: ev.Type,
		DocID:     doc.ID,
		DocIRI:    subject,
		DocKind:   doc.Kind,
		DocTitle:  doc.Title,
		Version:   doc.Version,
		Project:   doc.Project,
	}
	// Only the guard the rule for this event type can consult: a
	// submission never looks at design tasks and an acceptance never at
	// review tasks, so fetching both would be a wasted round trip on
	// every event.
	switch ev.Type {
	case eventbus.TypeDocumentSubmitted:
		in.OpenReviewTask, err = s.st.OpenTaskForDoc(ctx, doc.ID, "review")
	case eventbus.TypeDocumentAccepted:
		in.OpenDesignTask, err = s.st.OpenTaskForDoc(ctx, doc.ID, "design")
	}
	if err != nil {
		return eventbus.OutcomeApplied, fmt.Errorf("doc-lifecycle: event %d: %w", ev.ID, err)
	}

	outcome := eventbus.OutcomeApplied
	for _, act := range watcher.Evaluate(in) {
		if err := s.performDocAction(ctx, ev, doc, act); err != nil {
			s.watcherMetrics.Action(act.Rule, string(eventbus.OutcomeError))
			return outcome, err
		}
		if act.Suppressed {
			s.watcherMetrics.Action(act.Rule, string(eventbus.OutcomeSuppressed))
			outcome = eventbus.OutcomeSuppressed
			continue
		}
		s.watcherMetrics.Action(act.Rule, string(eventbus.OutcomeApplied))
	}
	return outcome, nil
}

// performDocAction carries out one rule decision.
//
// A suppression with no NoteTask records nothing at all: there is no entity
// whose timeline the absence of a mint belongs on, and the metric already
// counts it. A suppression with one notes the absorbed event on that task,
// so the timeline of the open design task shows the acceptances it stands
// for rather than losing them (025 §15.4).
func (s *server) performDocAction(ctx context.Context, ev store.Event, doc *model.Doc, act watcher.Action) error {
	if act.Suppressed {
		if act.NoteTask == "" {
			return nil
		}
		payload, err := json.Marshal(map[string]any{
			"rule":               act.Rule,
			"task":               act.NoteTask,
			"absorbed_event":     ev.ID,
			"prov:wasInformedBy": eventIRI(ev.ID),
		})
		if err != nil {
			return fmt.Errorf("doc-lifecycle: marshal note payload: %w", err)
		}
		_, _, err = s.st.RecordEvent(ctx, watcherEventSource,
			"doc-lifecycle:note:"+strconv.FormatInt(ev.ID, 10), "task.updated", payload,
			func(tx *sql.Tx, eventID int64) error {
				return store.LogChange(tx, "task", act.NoteTask, eventID,
					map[string]any{"absorbed_event": ev.ID, "type": ev.Type})
			})
		if err != nil {
			return fmt.Errorf("doc-lifecycle: note absorbed event %d on %s: %w", ev.ID, act.NoteTask, err)
		}
		return nil
	}

	// The payload names the rule and the document; the minted task id is
	// added by store.AttributeEventToTask inside apply, because RecordEvent
	// marshals the payload before the transaction opens and the id is
	// allocated from the project counter inside it (025 §15.2). CreateTask
	// also writes a state_log row for the new task attributed to this event,
	// so "why does this task exist" reads task → task.created event →
	// prov:wasInformedBy → the document event from either direction.
	payload, err := json.Marshal(map[string]any{
		"rule":               act.Rule,
		"doc":                store.DocIRI(*doc),
		"kind":               act.TaskKind,
		"prov:wasInformedBy": eventIRI(ev.ID),
	})
	if err != nil {
		return fmt.Errorf("doc-lifecycle: marshal mint payload: %w", err)
	}
	_, _, err = s.st.RecordEvent(ctx, watcherEventSource,
		"doc-lifecycle:"+act.Rule+":"+strconv.FormatInt(ev.ID, 10), "task.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			// CreateTask writes that state_log row itself, attributed to this
			// event id, so there is no second LogChange here — the task's
			// timeline already starts at the mint.
			t, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
				ProjectID: doc.Project,
				Title:     act.Title,
				Body:      act.Body,
				Kind:      act.TaskKind,
				Priority:  "medium",
				AboutDoc:  doc.ID,
				CreatedBy: watcherActorID,
			}, eventID)
			if err != nil {
				return err
			}
			return store.AttributeEventToTask(tx, eventID, t.ID)
		})
	if err != nil {
		return fmt.Errorf("doc-lifecycle: mint %s task for event %d: %w", act.TaskKind, ev.ID, err)
	}
	return nil
}

// eventSubject reads wl:subject out of a typed event's payload. A payload
// that is not an object, or that carries no string wl:subject, is a
// malformed typed event: eventbus.Emit validates the property set at emit
// time, so reaching the subscriber without one is a bug upstream, not an
// event to skip.
func eventSubject(ev store.Event) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return "", fmt.Errorf("doc-lifecycle: event %d payload: %w", ev.ID, err)
	}
	subject, _ := payload["wl:subject"].(string)
	if subject == "" {
		return "", fmt.Errorf("doc-lifecycle: event %d (%s) carries no wl:subject", ev.ID, ev.Type)
	}
	return subject, nil
}

// eventIRI renders 025 §15.2's identifier for one event row.
func eventIRI(id int64) string { return "wlid:event/" + strconv.FormatInt(id, 10) }
