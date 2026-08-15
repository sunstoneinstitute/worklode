package eventbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestEmitDocumentAcceptedRoundTrip(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()

	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	ev := DocumentAccepted{
		Doc:     "wlid:doc/spec-025",
		Actor:   "stig",
		At:      at,
		Version: 2,
		From:    "wlc:draft",
		To:      "wlc:accepted",
	}

	id, inserted, err := Emit(ctx, s, "cli", ev, nil)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !inserted {
		t.Fatalf("Emit: want inserted=true, got false")
	}

	e, err := s.GetEvent(ctx, id)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.ID != id {
		t.Fatalf("event id: got %d, want %d", e.ID, id)
	}
	if e.Type != "wl:DocumentAccepted" {
		t.Fatalf("event type: got %q, want wl:DocumentAccepted", e.Type)
	}
	if e.ExternalID != "wl:DocumentAccepted:wlid:doc/spec-025:2" {
		t.Fatalf("external_id: got %q", e.ExternalID)
	}

	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["@type"] != "wl:DocumentAccepted" {
		t.Fatalf("@type: got %v", payload["@type"])
	}
	wantAtID := fmt.Sprintf("wlid:event/%d", id)
	if payload["@id"] != wantAtID {
		t.Fatalf("@id: got %v, want %v", payload["@id"], wantAtID)
	}
	if payload["wl:subject"] != "wlid:doc/spec-025" {
		t.Fatalf("wl:subject: got %v", payload["wl:subject"])
	}
	if payload["prov:wasAssociatedWith"] != "wlid:actor/stig" {
		t.Fatalf("prov:wasAssociatedWith: got %v", payload["prov:wasAssociatedWith"])
	}
	if payload["prov:atTime"] != at.Format(time.RFC3339) {
		t.Fatalf("prov:atTime: got %v, want %v", payload["prov:atTime"], at.Format(time.RFC3339))
	}
	if payload["wl:fromStatus"] != "wlc:draft" {
		t.Fatalf("wl:fromStatus: got %v", payload["wl:fromStatus"])
	}
	if payload["wl:toStatus"] != "wlc:accepted" {
		t.Fatalf("wl:toStatus: got %v", payload["wl:toStatus"])
	}
}

func TestEmitIdempotentAtTheLog(t *testing.T) {
	s := store.OpenTestStore(t)
	ctx := t.Context()

	ev := DocumentAccepted{
		Doc:     "wlid:doc/spec-025",
		Actor:   "stig",
		At:      time.Now().UTC(),
		Version: 1,
		From:    "wlc:draft",
		To:      "wlc:accepted",
	}

	id1, inserted1, err := Emit(ctx, s, "cli", ev, nil)
	if err != nil {
		t.Fatalf("first Emit: %v", err)
	}
	if !inserted1 {
		t.Fatalf("first Emit: want inserted=true, got false")
	}

	id2, inserted2, err := Emit(ctx, s, "cli", ev, nil)
	if err != nil {
		t.Fatalf("second Emit: %v", err)
	}
	if inserted2 {
		t.Fatalf("second Emit: want inserted=false, got true")
	}
	if id2 != id1 {
		t.Fatalf("second Emit: want same id %d, got %d", id1, id2)
	}
}

func TestValidatePayloadRejectsUnknownProperty(t *testing.T) {
	keys := []string{"prov:atTime", "prov:wasAssociatedWith", "wl:subject", "wl:fromStatus", "wl:toStatus", "wl:bogus"}
	err := validatePayload(TypeDocumentAccepted, keys)
	if err == nil {
		t.Fatalf("validatePayload: want error for unknown property, got nil")
	}
	if !strings.Contains(err.Error(), "wl:bogus") {
		t.Fatalf("validatePayload error: want it to name wl:bogus, got %q", err.Error())
	}
}
