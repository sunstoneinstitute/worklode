package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestStripHeaderExpression runs the expression of migration
// NEW-strip_headers (copied here; keep the two in step) over the body shapes
// it has to handle.
func TestStripHeaderExpression(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	const strip = `SELECT CASE WHEN $1 LIKE E'---\n%' AND position(E'\n---\n' IN substr($1, 4)) > 0
	                    THEN ltrim(substr($1, 4 + position(E'\n---\n' IN substr($1, 4)) + 4), E'\n')
	                    ELSE $1 END`
	cases := []struct{ name, in, want string }{
		{"header then H1", "---\nstatus: draft\nissued: 2026-08-01\n---\n\n# Title\n\nBody.\n", "# Title\n\nBody.\n"},
		{"empty header", "---\n---\n# Title\n", "# Title\n"},
		{"no header", "# Title\n\n---\n\nBody.\n", "# Title\n\n---\n\nBody.\n"},
		{"opening rule, no closing fence", "---\n# Title\n\nBody.\n", "---\n# Title\n\nBody.\n"},
	}
	for _, c := range cases {
		var got string
		if err := s.db.QueryRowContext(t.Context(), strip, c.in).Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestCreateDocStoresNoHeader: CreateDoc still reads a header, writes the
// rows it states, and stores the body without it.
func TestCreateDocStoresNoHeader(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	if !strings.HasPrefix(doc.Body, "# Documents in the backbone") {
		t.Errorf("stored body = %q, want it to open with the H1", doc.Body)
	}
	if doc.Issued != "2026-08-01" || doc.Title != "Documents in the backbone" {
		t.Errorf("issued, title = %q, %q; want the header's date and the H1", doc.Issued, doc.Title)
	}
	if edges := docEdges(t, s, doc.ID); !slices.ContainsFunc(edges, func(e model.DocEdge) bool { return e.Type == "requires" }) {
		t.Errorf("edges = %+v, want the header's requires", edges)
	}
}

// TestBodyWritesRefuseHeader: every body write after creation refuses a
// header — a draft edit, a candidate update, and an in-place patch.
func TestBodyWritesRefuseHeader(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	isRefusal := func(err error) bool {
		return errors.Is(err, ErrInvalidInput) && strings.Contains(err.Error(), "carries no header")
	}

	draft := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 30, Slug: "030-draft", Body: specBody, CreatedBy: "stig",
	})
	write := func(apply func(tx *sql.Tx, eventID int64) error) error {
		_, _, err := s.RecordDocEvent(t.Context(), "update", "cli",
			fmt.Sprintf("doc-raw-%d", docEventSeq.Add(1)), "doc.update", nil, apply)
		return err
	}
	if err := write(func(tx *sql.Tx, eventID int64) error {
		_, err := UpdateDocBody(tx, s.Now(), draft.ID, specBody, 0, eventID)
		return err
	}); !isRefusal(err) {
		t.Errorf("body edit with a header = %v, want the header refusal", err)
	}

	accepted := mustAcceptedSpec(t, s, "025-x")
	if err := reviseDoc(t, s, accepted.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := write(func(tx *sql.Tx, eventID int64) error {
		return UpdateRevision(tx, s.Now(), accepted.ID, revisedSpecBody, eventID)
	}); !isRefusal(err) {
		t.Errorf("candidate update with a header = %v, want the header refusal", err)
	}

	patched := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 31, Slug: "031-patched", Body: patchSpecBody,
		CreatedBy: "stig", Status: "accepted",
	})
	if _, _, err := patchDoc(t, s, DocPatchInput{
		ID: patched.ID, Body: "---\nstatus: accepted\n---\n" + reword(patchSpecBody), Note: "n", ActorID: "stig",
	}); !isRefusal(err) {
		t.Errorf("patch with a header = %v, want the header refusal", err)
	}
}

// TestBodyEditKeepsTitle: the H1 no longer sets docs.title, and a body edit
// moves neither issued nor the edges.
func TestBodyEditKeepsTitle(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	before := docEdges(t, s, doc.ID)
	body := strings.Replace(noHeader(t, specBody), "# Documents in the backbone", "# A new heading", 1)
	got, err := updateDocBody(t, s, doc.ID, body)
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if got.Title != doc.Title || got.Issued != doc.Issued {
		t.Errorf("title, issued = %q, %q; want %q, %q unchanged", got.Title, got.Issued, doc.Title, doc.Issued)
	}
	if after := docEdges(t, s, doc.ID); !reflect.DeepEqual(after, before) {
		t.Errorf("edges = %+v, want %+v unchanged", after, before)
	}
}

// TestSetDocColumns: title and issued are set by name, checked, logged, and
// move no version; a superseded document refuses.
func TestSetDocColumns(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	set := func(id int64, in model.DocColumnsInput) (*model.Doc, error) {
		var out *model.Doc
		_, _, err := s.RecordDocEvent(t.Context(), "update", "cli",
			fmt.Sprintf("doc-columns-%d", docEventSeq.Add(1)), "doc.columns_set", nil,
			func(tx *sql.Tx, eventID int64) error {
				var err error
				out, err = SetDocColumns(tx, s.Now(), id, in, eventID)
				return err
			})
		return out, err
	}
	title, issued := "Renamed", "2026-09-01"
	got, err := set(doc.ID, model.DocColumnsInput{Title: &title, Issued: &issued})
	if err != nil {
		t.Fatalf("SetDocColumns: %v", err)
	}
	if got.Title != title || got.Issued != issued || got.Version != doc.Version {
		t.Errorf("doc = {title:%q issued:%q version:%d}, want {%q %q %d}",
			got.Title, got.Issued, got.Version, title, issued, doc.Version)
	}
	var logged int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM state_log WHERE entity_kind = 'doc' AND entity_id = $1
		    AND change->>'field' IN ('title', 'issued')`, fmt.Sprint(doc.ID)).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != 2 {
		t.Errorf("state_log title/issued rows = %d, want 2", logged)
	}

	empty, bad := " ", "01/09/2026"
	for _, in := range []model.DocColumnsInput{{Title: &empty}, {Issued: &bad}, {}} {
		if _, err := set(doc.ID, in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("SetDocColumns(%+v) = %v, want ErrInvalidInput", in, err)
		}
	}
	setDocStatus(t, s, doc.ID, "superseded")
	if _, err := set(doc.ID, model.DocColumnsInput{Title: &title}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SetDocColumns on a superseded doc = %v, want ErrInvalidInput", err)
	}
}

// TestLinkOnPlanBumpsVersion: a plan's link is its next version, and the
// version it replaces keeps the old edge set.
func TestLinkOnPlanBumpsVersion(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody, CreatedBy: "stig"})
	before := docEdges(t, s, plan.ID)
	if err := linkDocEdge(t, s, plan.ID, model.DocEdgeInput{Type: "requires", To: spec.Slug + "#sec-1"}); err != nil {
		t.Fatalf("LinkDocEdge: %v", err)
	}
	got, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != plan.Version+1 {
		t.Errorf("version = %d, want %d", got.Version, plan.Version+1)
	}
	if !slices.ContainsFunc(docEdges(t, s, plan.ID), func(e model.DocEdge) bool {
		return e.Type == "requires" && e.ToDoc == spec.ID && e.ToAnchor == "sec-1"
	}) {
		t.Errorf("edges = %+v, want requires %s#sec-1", docEdges(t, s, plan.ID), spec.Slug)
	}
	v1, err := s.GetDocVersion(t.Context(), plan.ID, plan.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Edges) != len(before) {
		t.Errorf("version %d edges = %+v, want the old set %+v", plan.Version, v1.Edges, before)
	}
}

// TestLinkOnDraftSpecInPlace: a draft spec's link writes doc_edges with no
// version move; a second link is ErrEdgeExists, and unlink removes it once.
func TestLinkOnDraftSpecInPlace(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	other := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-other", Body: specBody, CreatedBy: "stig",
	})
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x", Body: specBody, CreatedBy: "stig",
	})
	edge := model.DocEdgeInput{Type: "requires", To: "026-other#sec-2"}
	has := func() bool {
		return slices.ContainsFunc(docEdges(t, s, doc.ID), func(e model.DocEdge) bool {
			return e.Type == "requires" && e.ToDoc == other.ID && e.ToAnchor == "sec-2"
		})
	}
	if err := linkDocEdge(t, s, doc.ID, edge); err != nil {
		t.Fatalf("LinkDocEdge: %v", err)
	}
	got, err := s.GetDoc(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !has() || got.Version != doc.Version {
		t.Errorf("edge present = %v, version = %d; want true, %d", has(), got.Version, doc.Version)
	}
	if err := linkDocEdge(t, s, doc.ID, edge); !errors.Is(err, ErrEdgeExists) {
		t.Errorf("second link = %v, want ErrEdgeExists", err)
	}
	if err := unlinkDocEdge(t, s, doc.ID, edge); err != nil {
		t.Fatalf("UnlinkDocEdge: %v", err)
	}
	if has() {
		t.Error("edge still present after unlink")
	}
	if err := unlinkDocEdge(t, s, doc.ID, edge); !errors.Is(err, ErrNotFound) {
		t.Errorf("second unlink = %v, want ErrNotFound", err)
	}
}

// TestLinkOnAcceptedSpecWritesCandidate: an accepted spec's link opens a
// candidate revision authored by the actor and writes its edge set; the live
// set moves when the candidate lands.
func TestLinkOnAcceptedSpecWritesCandidate(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	other := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-other", Body: specBody, CreatedBy: "stig",
	})
	doc := mustAcceptedSpec(t, s, "025-x")
	before := docEdges(t, s, doc.ID)
	if err := linkDocEdge(t, s, doc.ID, model.DocEdgeInput{Type: "requires", To: "026-other"}); err != nil {
		t.Fatalf("LinkDocEdge: %v", err)
	}
	rev, err := s.GetDocRevision(t.Context(), doc.ID)
	if err != nil {
		t.Fatalf("GetDocRevision: %v", err)
	}
	isNew := func(e model.DocEdge) bool { return e.Type == "requires" && e.ToDoc == other.ID }
	if rev.CreatedBy != "stig" || !slices.ContainsFunc(rev.Edges, isNew) {
		t.Errorf("revision = {by:%q edges:%+v}, want by stig with requires 026-other", rev.CreatedBy, rev.Edges)
	}
	if live := docEdges(t, s, doc.ID); !reflect.DeepEqual(live, before) {
		t.Errorf("live edges = %+v, want %+v until the candidate lands", live, before)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}
	after, _, err := s.ListDocEdges(t.Context(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(after, isNew) {
		t.Errorf("edges after accept = %+v, want requires 026-other", after)
	}
}

// TestLinkRefusesInverse: an inverse spelling is refused naming the declared
// type (WL-SPEC-77 §8.1), as is a type no table declares and a document past
// changing.
func TestLinkRefusesInverse(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "a-plan", Body: planBody, CreatedBy: "stig"})
	for typ, declared := range map[string]string{"blocks": "blockedBy", "isRequiredBy": "requires"} {
		err := linkDocEdge(t, s, plan.ID, model.DocEdgeInput{Type: typ, To: "a-plan"})
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), declared) ||
			!strings.Contains(err.Error(), "WL-SPEC-77 §8.1") {
			t.Errorf("link %s = %v, want ErrInvalidInput naming %s and WL-SPEC-77 §8.1", typ, err, declared)
		}
	}
	for _, typ := range []string{"bogus", "implements"} {
		if err := linkDocEdge(t, s, plan.ID, model.DocEdgeInput{Type: typ, To: "x"}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("link %s = %v, want ErrInvalidInput", typ, err)
		}
	}
	setDocStatus(t, s, plan.ID, "spent")
	if err := linkDocEdge(t, s, plan.ID, model.DocEdgeInput{Type: "wasDerivedFrom", To: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("link on a spent plan = %v, want ErrInvalidInput", err)
	}
}
