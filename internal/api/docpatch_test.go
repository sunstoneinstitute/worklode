package api_test

// docpatch_test.go covers POST /api/v1/docs/{id}/patch: 025 §8.4's in-place
// amendment of an accepted spec. The rule split itself is the store's and is
// tested there; this is about the route, the response and what the
// doc.patched event says.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestPatchDocRoute: a non-substantive patch lands, answers with the document
// and what the amendment did, and leaves a doc.patched event naming the
// sections it changed.
func TestPatchDocRoute(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-patch", 25)

	edited := strings.Replace(docSpecBody, "Scope body.", "Scope body, restated.", 1)
	rr := doReq(t, h, "POST", docPath(spec.ID, "/patch"), token,
		model.PatchDocInput{Body: edited, Note: "clarified the scope"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.DocPatchResponse
	decodeInto(t, rr, &got)
	if got.Doc.Status != "accepted" || got.Doc.Version != 2 {
		t.Errorf("doc = %s v%d, want accepted v2", got.Doc.Status, got.Doc.Version)
	}
	if got.Patch.Classification != "non-substantive" || got.Patch.RuleFired != "none" ||
		len(got.Patch.ChangedAnchors) != 1 || got.Patch.ChangedAnchors[0] != "sec-1" {
		t.Errorf("patch = %+v, want a non-substantive sec-1 amendment", got.Patch)
	}

	// §15.5: the event says what the amendment did, which the handler can
	// only write once the store has done it.
	var payload struct {
		Anchors        []string `json:"anchors"`
		Classification string   `json:"classification"`
		Rule           string   `json:"rule"`
	}
	if err := json.Unmarshal(eventOfType(t, st, "doc.patched").Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Anchors) != 1 || payload.Anchors[0] != "sec-1" ||
		payload.Classification != "non-substantive" || payload.Rule != "none" {
		t.Errorf("doc.patched payload = %+v, want sec-1/non-substantive/none", payload)
	}
}

// eventOfType finds the one event of a type in a test's freshly created
// database, reading by id rather than through ListEvents: the log's cursor
// reads are commit-horizon bounded, and a transaction open in another test
// holds that horizon behind an event this one just committed.
func eventOfType(t *testing.T, st *store.Store, typ string) store.Event {
	t.Helper()
	for id := int64(1); id <= 50; id++ {
		e, err := st.GetEvent(t.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("no %s event recorded", typ)
	return store.Event{}
}

// TestPatchDocRefusedIsUnprocessable: a mechanical rule refuses the edit with
// the rule named and the document untouched. The route holds no rule of its
// own — the message is the store's (§8.3).
func TestPatchDocRefusedIsUnprocessable(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-refused", 25)

	edited := strings.Replace(docSpecBody, "requires: 004-execution-backbone.md#sec-6",
		"requires:\n  - 004-execution-backbone.md#sec-6\n  - 022-metrics.md#sec-1", 1)
	rr := doReq(t, h, "POST", docPath(spec.ID, "/patch"), token,
		model.PatchDocInput{Body: edited, Note: "one more dependency"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "new-dependency") {
		t.Errorf("body = %s, want the rule named", rr.Body.String())
	}
	after, err := st.GetDoc(t.Context(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != 1 {
		t.Errorf("version = %d, want the refused patch to have changed nothing", after.Version)
	}
}
