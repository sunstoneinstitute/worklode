package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

// randomID returns a random hex string, for driving RecordEvent's
// (source, externalID) idempotency key in tests that don't otherwise care
// about the external id.
func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// syncDocs drives ApplyDocSync the way the API will: through RecordEvent.
func syncDocs(t *testing.T, s *Store, project string, prov DocSyncProvenance,
	docs []DocUpsert) []DocSyncResult {
	t.Helper()
	res, err := syncDocsErr(s, project, prov, docs)
	if err != nil {
		t.Fatalf("ApplyDocSync: %v", err)
	}
	return res
}

func syncDocsErr(s *Store, project string, prov DocSyncProvenance,
	docs []DocUpsert) ([]DocSyncResult, error) {
	var res []DocSyncResult
	_, _, err := s.RecordEvent(context.Background(), "cli", randomID(), "docs.synced", []byte(`{}`),
		func(tx *sql.Tx, eventID int64) error {
			var err error
			res, err = s.ApplyDocSync(tx, s.Now(), eventID, project, prov, docs)
			return err
		})
	return res, err
}

func specUpsert() DocUpsert {
	return DocUpsert{
		Kind: "spec", Ordinal: "34", Status: "accepted",
		Title:       "Spec 034 — Design-doc sync",
		Body:        "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		Frontmatter: json.RawMessage(`{"status":"accepted"}`),
		Sections:    []DocSection{{Anchor: "sec-1", Heading: "Scope", Depth: 2, Position: 0}},
		Edges: []DocEdge{{SrcAnchor: "sec-1", Rel: "amends",
			Target: "025-documents-in-the-backbone.md", TargetAnchor: "sec-2"}},
	}
}

func planUpsert() DocUpsert {
	return DocUpsert{
		Kind: "plan", Ordinal: "34-1", Status: "draft", Title: "Part 1",
		Body:        "---\nstatus: draft\n---\n# Part 1\n",
		Frontmatter: json.RawMessage(`{"status":"draft","implements":"docs/specs/034-design-doc-sync.md"}`),
		Edges:       []DocEdge{{Rel: "implements", Target: "docs/specs/034-design-doc-sync.md"}},
	}
}

func TestApplyDocSyncAddUpdateUnchanged(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}

	res := syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert(), planUpsert()})
	if len(res) != 2 || res[0].Outcome != "added" || res[1].Outcome != "added" {
		t.Fatalf("first sync = %+v, want two added", res)
	}
	if res[0].DocID != "WL-SPEC-34" || res[1].DocID != "WL-PLAN-34-1" {
		t.Fatalf("doc ids = %q, %q", res[0].DocID, res[1].DocID)
	}

	// Same content, same key order or not: unchanged, version still 1,
	// provenance overwritten.
	forced := DocSyncProvenance{SourceBranch: "feature-x", Dirty: true}
	res = syncDocs(t, s, "wl", forced, []DocUpsert{specUpsert(), planUpsert()})
	for _, r := range res {
		if r.Outcome != "unchanged" {
			t.Errorf("%s outcome = %q, want unchanged", r.DocID, r.Outcome)
		}
	}
	d, _, _, err := s.GetDoc(ctx, "WL-SPEC-34")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.SourceBranch != "feature-x" || !d.SourceDirty {
		t.Errorf("after unchanged sync: version=%d branch=%q dirty=%v; want 1, feature-x, true",
			d.Version, d.SourceBranch, d.SourceDirty)
	}

	// Changed body: updated, version bumped, sections replaced.
	changed := specUpsert()
	changed.Body += "\n## 2. More {#sec-2}\n"
	changed.Sections = append(changed.Sections,
		DocSection{Anchor: "sec-2", Heading: "More", Depth: 2, Position: 1})
	res = syncDocs(t, s, "wl", prov, []DocUpsert{changed})
	if res[0].Outcome != "updated" {
		t.Fatalf("outcome = %q, want updated", res[0].Outcome)
	}
	d, secs, _, err := s.GetDoc(ctx, "WL-SPEC-34")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 2 || len(secs) != 2 {
		t.Errorf("after update: version=%d sections=%d; want 2, 2", d.Version, len(secs))
	}
}

func TestApplyDocSyncValidation(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	bad := func(mutate func(*DocUpsert)) error {
		d := specUpsert()
		mutate(&d)
		_, err := syncDocsErr(s, "wl", DocSyncProvenance{SourceBranch: "main"}, []DocUpsert{d})
		return err
	}
	for name, tc := range map[string]func(*DocUpsert){
		"bad kind":             func(d *DocUpsert) { d.Kind = "memo" },
		"bad spec ordinal":     func(d *DocUpsert) { d.Ordinal = "034" },
		"plan ordinal on spec": func(d *DocUpsert) { d.Ordinal = "34-1" },
		"empty status":         func(d *DocUpsert) { d.Status = "" },
		"empty title":          func(d *DocUpsert) { d.Title = "" },
		"bad edge rel":         func(d *DocUpsert) { d.Edges[0].Rel = "mentions" },
	} {
		if err := bad(tc); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := syncDocsErr(s, "nope", DocSyncProvenance{}, []DocUpsert{specUpsert()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: err = %v, want ErrNotFound", err)
	}
}

func TestApplyDocSyncWritesStateLog(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()})
	syncDocs(t, s, "wl", prov, []DocUpsert{specUpsert()}) // unchanged: no new row

	entries, err := s.StateLogForEntity(ctx, "doc", "WL-SPEC-34")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("state_log rows = %d, want 1 (added only; unchanged logs nothing)", len(entries))
	}
}

func TestDocSyncOutcomesWritesNothing(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	res, err := s.DocSyncOutcomes(ctx, "wl", []DocUpsert{specUpsert()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Outcome != "added" {
		t.Fatalf("dry-run = %+v, want one added", res)
	}
	if _, _, _, err := s.GetDoc(ctx, "WL-SPEC-34"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry run wrote a doc: GetDoc err = %v, want ErrNotFound", err)
	}
}
