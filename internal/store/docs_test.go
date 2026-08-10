package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
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

	// Byte-identical content re-synced: unchanged, version still 1,
	// provenance overwritten. (Key-order independence of the jsonb compare
	// is a separate property, proven by TestApplyDocSyncJSONKeyOrderUnchanged.)
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

// TestApplyDocSyncJSONKeyOrderUnchanged proves the unchanged/updated compare
// is done in jsonb (frontmatter = $n::jsonb), not as a byte/text comparison:
// re-syncing the same doc with the same frontmatter keys in a different
// order must still land as "unchanged" with no version bump.
func TestApplyDocSyncJSONKeyOrderUnchanged(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	prov := DocSyncProvenance{SourceBranch: "main"}

	d := specUpsert()
	d.Frontmatter = json.RawMessage(`{"status":"accepted","title":"x"}`)
	res := syncDocs(t, s, "wl", prov, []DocUpsert{d})
	if res[0].Outcome != "added" {
		t.Fatalf("first sync outcome = %q, want added", res[0].Outcome)
	}

	reordered := d
	reordered.Frontmatter = json.RawMessage(`{"title":"x","status":"accepted"}`)
	res = syncDocs(t, s, "wl", prov, []DocUpsert{reordered})
	if res[0].Outcome != "unchanged" {
		t.Fatalf("reordered-keys sync outcome = %q, want unchanged (jsonb compare should ignore key order)",
			res[0].Outcome)
	}

	got, _, _, err := s.GetDoc(ctx, "WL-SPEC-34")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1 (unchanged must not bump version)", got.Version)
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
		"nil frontmatter":      func(d *DocUpsert) { d.Frontmatter = nil },
		"empty frontmatter":    func(d *DocUpsert) { d.Frontmatter = json.RawMessage("") },
		"invalid frontmatter":  func(d *DocUpsert) { d.Frontmatter = json.RawMessage("{") },
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

func TestGetDocDetail(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	syncDocs(t, s, "wl", DocSyncProvenance{SourceBranch: "main"},
		[]DocUpsert{specUpsert(), planUpsert()})

	d, secs, edges, err := s.GetDoc(ctx, "WL-SPEC-34")
	if err != nil {
		t.Fatal(err)
	}
	if d.DocID != "WL-SPEC-34" || d.Kind != "spec" || d.Ordinal != "34" ||
		d.Status != "accepted" || d.Body == "" || d.Version != 1 {
		t.Errorf("doc = %+v", d)
	}
	if len(secs) != 1 || secs[0].Anchor != "sec-1" || secs[0].Heading != "Scope" {
		t.Errorf("sections = %+v", secs)
	}
	if len(edges) != 1 || edges[0].Rel != "amends" || edges[0].TargetAnchor != "sec-2" {
		t.Errorf("edges = %+v", edges)
	}

	if _, _, _, err := s.GetDoc(ctx, "WL-SPEC-999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing doc: err = %v, want ErrNotFound", err)
	}
}

func TestListDocsFiltersAndOrder(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "wl", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	nine := specUpsert()
	nine.Ordinal, nine.Status = "9", "draft"
	ten := specUpsert()
	ten.Ordinal = "10"
	p2 := planUpsert()
	p2.Ordinal = "34-2"
	syncDocs(t, s, "wl", DocSyncProvenance{SourceBranch: "main"},
		[]DocUpsert{ten, nine, specUpsert(), planUpsert(), p2})

	all, err := s.ListDocs(ctx, DocFilter{Project: "wl"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range all {
		ids = append(ids, d.DocID)
		if d.Body != "" {
			t.Errorf("%s: list row carries a body", d.DocID)
		}
	}
	want := []string{"WL-PLAN-34-1", "WL-PLAN-34-2", "WL-SPEC-9", "WL-SPEC-10", "WL-SPEC-34"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("order = %v, want %v (numeric ordinal order, 9 before 10)", ids, want)
	}

	drafts, err := s.ListDocs(ctx, DocFilter{Project: "wl", Kind: "spec", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].DocID != "WL-SPEC-9" {
		t.Errorf("filtered = %+v, want just WL-SPEC-9", drafts)
	}
}
