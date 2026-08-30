package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// runtimeEventBody returns a valid runtime-event request body; override
// fields per test.
func runtimeEventBody() map[string]any {
	return map[string]any{
		"cluster":     "dev",
		"kind":        "crashloop",
		"workload":    "ns1/app",
		"image":       "registry.example.com/sunstone/app:v1.2.3",
		"message":     "container app in CrashLoopBackOff (restarts: 5)",
		"occurred_at": "2026-07-19T12:00:00Z",
		"dedupe_key":  "dev/uid-1/app/crashloop/5",
	}
}

func TestRuntimeEventsRequireAuth(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/runtime-events", "", runtimeEventBody())
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRuntimeEventsValidation(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)

	t.Run("bad kind", func(t *testing.T) {
		body := runtimeEventBody()
		body["kind"] = "flux_failure"
		rr := doReq(t, h, "POST", "/api/v1/runtime-events", token, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing dedupe_key", func(t *testing.T) {
		body := runtimeEventBody()
		body["dedupe_key"] = ""
		rr := doReq(t, h, "POST", "/api/v1/runtime-events", token, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("bad occurred_at", func(t *testing.T) {
		body := runtimeEventBody()
		body["occurred_at"] = "yesterday"
		rr := doReq(t, h, "POST", "/api/v1/runtime-events", token, body)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
		}
	})
}

func TestRuntimeEventsCreateAndDeduplicate(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)

	// A docker_image artifact matching the reported image name:tag — the
	// store should resolve it onto the runtime event.
	var artifactID int64
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		artifactID, err = store.CreateArtifact(tx, store.Artifact{
			Kind:      "docker_image",
			Name:      "registry.example.com/sunstone/app",
			Version:   "v1.2.3",
			SourceSHA: "abc123",
		})
		return err
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	rr := doReq(t, h, "POST", "/api/v1/runtime-events", token, runtimeEventBody())
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	created := decodeMap(t, rr)
	if created["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", created["status"])
	}
	id, ok := created["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("id field = %v, want a positive number", created["id"])
	}

	events, err := st.ListRuntimeEvents(context.Background(), "dev", 0)
	if err != nil {
		t.Fatalf("list runtime events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("runtime events = %d, want 1", len(events))
	}
	got := events[0]
	if got.ID != int64(id) {
		t.Fatalf("row id = %d, want %d", got.ID, int64(id))
	}
	if got.Kind != "crashloop" || got.Cluster != "dev" || got.Workload != "ns1/app" {
		t.Fatalf("row = %+v, want crashloop/dev/ns1/app", got)
	}
	if got.ArtifactID == nil || *got.ArtifactID != artifactID {
		t.Fatalf("artifact_id = %v, want %d", got.ArtifactID, artifactID)
	}
	if got.OccurredAt.Format("2006-01-02T15:04:05Z") != "2026-07-19T12:00:00Z" {
		t.Fatalf("occurred_at = %v, want 2026-07-19T12:00:00Z", got.OccurredAt)
	}

	// Same dedupe_key again: duplicate delivery, no second row.
	rr = doReq(t, h, "POST", "/api/v1/runtime-events", token, runtimeEventBody())
	if rr.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["status"]; got != "duplicate" {
		t.Fatalf("duplicate status field = %v, want duplicate", got)
	}
	events, err = st.ListRuntimeEvents(context.Background(), "dev", 0)
	if err != nil {
		t.Fatalf("list runtime events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("runtime events after duplicate = %d, want 1", len(events))
	}
}
