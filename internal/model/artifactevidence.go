package model

import (
	"encoding/json"
	"time"
)

// ArtifactEvidence is one reported fact about a declared artifact address
// (spec 029 §3.2): an external system asserted this State at OccurredAt, and
// the event that carried it is the provenance. Nothing here is a human's
// claim — Provenance is "observed" for an emitter and "user_reported" for a
// person, and neither is a status the declaring entity stores.
type ArtifactEvidence struct {
	EntityKind string          `json:"entity_kind"` // deliverable | task | doc
	EntityID   string          `json:"entity_id"`
	Artifact   string          `json:"artifact"` // the declared address the fact is about
	Source     string          `json:"source"`   // the ingest source that reported it
	State      string          `json:"state"`    // published | updated | deprecated | removed | failed
	Provenance string          `json:"provenance"`
	Version    string          `json:"version"` // the emitter's version/snapshot id, "" if none
	URL        string          `json:"url"`
	Detail     json.RawMessage `json:"detail,omitempty"` // free-form emitter payload
	OccurredAt time.Time       `json:"occurred_at"`
}
