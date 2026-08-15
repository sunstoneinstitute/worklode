package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DomainEvent is one emittable event type (spec 025 §15.3): it knows its
// type curie, its deterministic external id, and its JSON-LD payload.
type DomainEvent interface {
	EventType() string
	// ExternalID is <type>:<subject>:<version> — what makes a retried
	// request idempotent at the log (025 §15.3).
	ExternalID() string
	// Properties returns the payload minus @context/@type/@id, keyed by
	// ontology property curie.
	Properties() map[string]any
}

// DocumentSubmitted records a document entering review (025 §15.3).
type DocumentSubmitted struct {
	Doc     string // subject IRI, e.g. wlid:doc/spec-025
	Actor   string // actor id; rendered wlid:actor/<id>
	At      time.Time
	Version int
}

func (e DocumentSubmitted) EventType() string { return TypeDocumentSubmitted }

func (e DocumentSubmitted) ExternalID() string {
	return fmt.Sprintf("%s:%s:%d", TypeDocumentSubmitted, e.Doc, e.Version)
}

func (e DocumentSubmitted) Properties() map[string]any {
	return map[string]any{
		"prov:atTime":            e.At.UTC().Format(time.RFC3339),
		"prov:wasAssociatedWith": "wlid:actor/" + e.Actor,
		"wl:subject":             e.Doc,
	}
}

// DocumentAccepted records a document's status transitioning to accepted
// (025 §15.3).
type DocumentAccepted struct {
	Doc      string
	Actor    string
	At       time.Time
	Version  int
	From, To string // wlc: status curies
}

func (e DocumentAccepted) EventType() string { return TypeDocumentAccepted }

func (e DocumentAccepted) ExternalID() string {
	return fmt.Sprintf("%s:%s:%d", TypeDocumentAccepted, e.Doc, e.Version)
}

func (e DocumentAccepted) Properties() map[string]any {
	return map[string]any{
		"prov:atTime":            e.At.UTC().Format(time.RFC3339),
		"prov:wasAssociatedWith": "wlid:actor/" + e.Actor,
		"wl:subject":             e.Doc,
		"wl:fromStatus":          e.From,
		"wl:toStatus":            e.To,
	}
}

// Emit validates ev's payload against the generated property set and
// records it through the store in one transaction with apply — the same
// seam every other write uses (025 §15.3: an event that could commit without
// its change is a log that lies).
func Emit(ctx context.Context, st *store.Store, source string, ev DomainEvent,
	apply func(tx *sql.Tx, eventID int64) error) (int64, bool, error) {
	props := ev.Properties()
	if err := validatePayload(ev.EventType(), keysOf(props)); err != nil {
		return 0, false, err
	}
	return st.RecordEventWithID(ctx, source, ev.ExternalID(), ev.EventType(),
		func(id int64) ([]byte, error) {
			full := map[string]any{
				"@context": "https://worklode.io/ns/ontology#",
				"@type":    ev.EventType(),
				"@id":      fmt.Sprintf("wlid:event/%d", id),
			}
			for k, v := range props {
				full[k] = v
			}
			return json.Marshal(full)
		}, apply)
}

// validatePayload checks that keys is a subset of the base properties plus
// typ's per-type properties, and that every per-type property for typ is
// present. A missing property fails at emit time (compile-time coverage
// comes from the struct; this is the runtime half, 025 §15.3).
func validatePayload(typ string, keys []string) error {
	extra, ok := payloadProperties[typ]
	if !ok {
		return fmt.Errorf("validate payload: unknown event type %q", typ)
	}
	allowed := make(map[string]bool, len(baseProperties)+len(extra))
	for _, p := range baseProperties {
		allowed[p] = true
	}
	for _, p := range extra {
		allowed[p] = true
	}

	for _, k := range keys {
		if !allowed[k] {
			return fmt.Errorf("validate payload for %s: unknown property %q", typ, k)
		}
	}
	for _, p := range extra {
		if !slices.Contains(keys, p) {
			return fmt.Errorf("validate payload for %s: missing required property %q", typ, p)
		}
	}
	return nil
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
