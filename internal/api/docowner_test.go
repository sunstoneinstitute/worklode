package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestListDocsByOwner: GET /api/v1/docs?owner= narrows to that owner's
// documents (025 §7.3, WL-382 task 4), served by the docs_owner partial
// index (migration 0058). It composes with the project and kind filters
// rather than replacing them, and an owner with no documents returns an
// empty list, not a 404.
func TestListDocsByOwner(t *testing.T) {
	st, h, token := newTestServer(t) // token's actor is alice
	createProject(t, st, "proj")
	docActor(t, st, "bob")

	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	aliceAdr := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "adr", Number: 1, Slug: "001-x", Body: docSpecBody,
	})
	bobSpec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-x", Body: docSpecBody, Owner: "bob",
	})

	cases := map[string]struct {
		query string
		want  []int64
	}{
		"by owner":      {"?owner=bob", []int64{bobSpec.ID}},
		"owner+kind":    {"?owner=alice&kind=adr", []int64{aliceAdr.ID}},
		"unknown owner": {"?owner=nobody", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "GET", "/api/v1/docs"+tc.query, token, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
			}
			var resp model.DocListResponse
			decodeInto(t, rr, &resp)
			var got []int64
			for _, d := range resp.Docs {
				got = append(got, d.ID)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTransferDocOwner walks POST /api/v1/docs/{id}/owner (025 §7.3): the
// owner or an admin may hand a document to another actor, a third party is
// refused, transferring to the actor that already owns it is a no-op that
// still answers 200, an unknown actor is a 422, and the transfer lands as a
// doc.owner_changed event shaped like every other document verb's — doc,
// actor, request — rather than the previous_owner/owner keys 025 §15.2
// originally proposed before this endpoint existed.
func TestTransferDocOwner(t *testing.T) {
	st, h, token := newTestServer(t) // token's actor is alice, admin=true
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")
	carolToken := docActor(t, st, "carol")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	if spec.Owner != "alice" {
		t.Fatalf("owner = %q, want alice (the creator)", spec.Owner)
	}

	// A third party (neither owner nor admin) is refused.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/owner"), bobToken,
		model.TransferDocOwnerInput{Owner: "bob"}); rr.Code != http.StatusForbidden {
		t.Fatalf("third party status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}

	// The owner transfers to bob.
	rr := doReq(t, h, "POST", docPath(spec.ID, "/owner"), token,
		model.TransferDocOwnerInput{Owner: "bob"})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner transfer status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if got.Owner != "bob" {
		t.Errorf("owner = %q, want bob", got.Owner)
	}

	// alice, an admin who no longer owns the document, transfers it to carol.
	rr = doReq(t, h, "POST", docPath(spec.ID, "/owner"), token,
		model.TransferDocOwnerInput{Owner: "carol"})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin transfer status = %d, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &got)
	if got.Owner != "carol" {
		t.Errorf("owner = %q, want carol", got.Owner)
	}

	// Transferring to the current owner is a no-op, not a refusal — Task 5's
	// bulk transfer loops this endpoint over many documents and relies on
	// re-runs being safe.
	rr = doReq(t, h, "POST", docPath(spec.ID, "/owner"), carolToken,
		model.TransferDocOwnerInput{Owner: "carol"})
	if rr.Code != http.StatusOK {
		t.Fatalf("self-transfer status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &got)
	if got.Owner != "carol" {
		t.Errorf("owner = %q, want carol", got.Owner)
	}

	// The new owner must be an existing actor.
	rr = doReq(t, h, "POST", docPath(spec.ID, "/owner"), carolToken,
		model.TransferDocOwnerInput{Owner: "nobody"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown actor status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	events := pollEvents(t, h, token, "?type=doc.owner_changed", 3)
	if len(events) != 3 {
		t.Fatalf("doc.owner_changed events = %d, want 3 (the two real transfers and the no-op)", len(events))
	}
	first, _ := events[0].(map[string]any)
	payload := eventPayload(t, first)
	if got, want := payload["doc"], float64(spec.ID); got != want {
		t.Errorf(`payload["doc"] = %v, want %v`, got, want)
	}
	if got := payload["actor"]; got != "alice" {
		t.Errorf(`payload["actor"] = %v, want "alice"`, got)
	}
	req, ok := payload["request"].(map[string]any)
	if !ok || req["owner"] != "bob" {
		t.Errorf(`payload["request"] = %v, want {"owner":"bob"}`, payload["request"])
	}
}
