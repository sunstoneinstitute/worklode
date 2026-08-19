package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// importCorpus is the fixture corpus: two specs (one accepted and amending a
// document later in the walk, one draft), an ADR, four plans covering the
// status and reference cases, and a specs/inlined/ subdirectory the walk must
// never descend into. Absolute, because TestMain chdirs the package out of its
// own directory (main_test.go).
func importCorpus() string { return filepath.Join(packageDir, "testdata", "import-corpus") }

// --- the walker, with no server -----------------------------------------

func TestWalkImportCorpus(t *testing.T) {
	docs, err := walkImportCorpus(importCorpus())
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	type want struct {
		kind   string
		number int
		status string
	}
	// Filename order, specs before plans. 900-generated-view.md lives under
	// specs/inlined/ and is absent by construction: the walk skips directories.
	expected := []struct {
		slug string
		want
	}{
		{"001-forward-spec", want{"spec", 1, "accepted"}},
		{"002-target-spec", want{"spec", 2, "draft"}},
		{"003-record-a-decision", want{"adr", 3, "draft"}},
		{"2026-01-01-mintable-plan", want{"plan", 0, "accepted"}},
		{"2026-01-02-legacy-plan", want{"plan", 0, "accepted"}},
		{"2026-01-03-task-key-plan", want{"plan", 0, "accepted"}},
		{"2026-01-04-cross-corpus-plan", want{"plan", 0, "draft"}},
	}
	if len(docs) != len(expected) {
		t.Fatalf("walked %d documents, want %d: %+v", len(docs), len(expected), docs)
	}
	for i, e := range expected {
		got := docs[i]
		if got.slug != e.slug {
			t.Fatalf("document %d = %q, want %q", i, got.slug, e.slug)
		}
		if got.kind != e.kind || got.number != e.number || got.status != e.status {
			t.Errorf("%s = {kind:%s number:%d status:%s}, want {kind:%s number:%d status:%s}",
				got.slug, got.kind, got.number, got.status, e.kind, e.number, e.status)
		}
		if got.body == "" || !strings.HasPrefix(got.body, "---\n") {
			t.Errorf("%s: body is not the file verbatim, frontmatter included", got.slug)
		}
	}
}

// TestWalkImportCorpusRejectsUnnumberedSpec: a spec or ADR is identified by its
// corpus number (025 §14.3), so a spec-directory file with none is a defect in
// the corpus, not a document to guess a number for.
func TestWalkImportCorpusRejectsUnnumberedSpec(t *testing.T) {
	dir := writeCorpus(t, map[string]string{"specs/no-number.md": "# A spec\n"})
	_, err := walkImportCorpus(dir)
	if err == nil || !strings.Contains(err.Error(), "no-number.md") {
		t.Fatalf("err = %v, want it to name the file", err)
	}
}

// TestWalkImportCorpusRejectsUnparseableFile: the whole import aborts on one
// bad file. A half-imported corpus is worse than an unimported one.
func TestWalkImportCorpusRejectsUnparseableFile(t *testing.T) {
	dir := writeCorpus(t, map[string]string{
		"specs/001-fine.md":   "---\nstatus: draft\n---\n\n# Fine\n",
		"specs/002-broken.md": "---\nnotAnOntologyTerm: x\n---\n\n# Broken\n",
	})
	_, err := walkImportCorpus(dir)
	if err == nil || !strings.Contains(err.Error(), "002-broken.md") {
		t.Fatalf("err = %v, want it to name the unparseable file", err)
	}
}

func TestWalkImportCorpusEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := walkImportCorpus(dir); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want it to name the empty corpus root", err)
	}
}

// TestUnresolvedImportRefs: every frontmatter reference resolves against the
// walked set except the cross-corpus one, including the forward reference the
// backbone cannot resolve until pass 2.
func TestUnresolvedImportRefs(t *testing.T) {
	docs, err := walkImportCorpus(importCorpus())
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	got := unresolvedImportRefs(docs)
	want := []unresolvedRef{{slug: "2026-01-04-cross-corpus-plan", ref: "rdf-registry:ADR-0006"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved = %+v, want %+v", got, want)
	}
}

// TestPrintUnresolvedRefsSeparatesTheSentinel: a plan declaring NO-SPEC is
// counted apart from a reference that was meant to resolve and did not. The
// real corpus carries eight of the former and none of the latter, and the
// cutover's go/no-go is read off exactly this output.
func TestPrintUnresolvedRefsSeparatesTheSentinel(t *testing.T) {
	var buf strings.Builder
	printUnresolvedRefs(&buf, []unresolvedRef{
		{slug: "a-plan", ref: noSpecSentinel},
		{slug: "b-plan", ref: "rdf-registry:ADR-0006"},
		{slug: "c-plan", ref: noSpecSentinel},
	})
	out := buf.String()
	for _, want := range []string{
		"b-plan: rdf-registry:ADR-0006",
		"1 reference(s) resolve to no document",
		"2 plan(s) declare NO-SPEC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "a-plan: ") || strings.Contains(out, "c-plan: ") {
		t.Errorf("the sentinel was listed as a dangling reference; got:\n%s", out)
	}
}

// writeCorpus materialises a corpus tree from path -> contents and returns its
// root.
func writeCorpus(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// --- the command, against a real store + server --------------------------

// importedDoc reads back one imported document by slug.
func importedDoc(t *testing.T, c *cli.Client, slug string) model.DocDetail {
	t.Helper()
	ctx := context.Background()
	id, err := resolveDocID(ctx, c, slug)
	if err != nil {
		t.Fatalf("resolve %s: %v", slug, err)
	}
	d, _, err := c.GetDoc(ctx, id)
	if err != nil {
		t.Fatalf("get %s: %v", slug, err)
	}
	return d
}

// TestDocImport drives the command over the fixture corpus once and then holds
// the result to the whole import contract. The test actor is an admin, which
// the import needs: stating a status and replacing an edge set are the
// doc.import permission.
func TestDocImport(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()

	out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", importCorpus())
	if err != nil {
		t.Fatalf("doc import: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "7 created") || !strings.Contains(out, "0 already present") {
		t.Fatalf("import summary = %q, want it to report 7 created and 0 skipped", out)
	}

	t.Run("two-pass import resolves the forward reference", func(t *testing.T) {
		target := importedDoc(t, c, "002-target-spec")
		src := importedDoc(t, c, "001-forward-spec")
		var amends []model.DocEdge
		for _, e := range src.Edges {
			if e.Type == "amends" {
				amends = append(amends, e)
			}
		}
		if len(amends) != 1 {
			t.Fatalf("amends edges = %+v, want exactly one", amends)
		}
		got := amends[0]
		if got.ToDoc != target.ID || got.ToExternal != "" {
			t.Errorf("amends edge = %+v, want to_doc %d and no to_external "+
				"(pass 2 must resolve a target created after the source)", got, target.ID)
		}
		if got.FromAnchor != "sec-2" || got.ToAnchor != "sec-1" {
			t.Errorf("amends edge anchors = %q -> %q, want sec-2 -> sec-1", got.FromAnchor, got.ToAnchor)
		}
	})

	t.Run("statuses", func(t *testing.T) {
		for slug, want := range map[string]string{
			"001-forward-spec":             "accepted",
			"002-target-spec":              "draft",
			"003-record-a-decision":        "draft",
			"2026-01-01-mintable-plan":     "accepted",
			"2026-01-02-legacy-plan":       "accepted", // no status key: a spent plan
			"2026-01-03-task-key-plan":     "accepted",
			"2026-01-04-cross-corpus-plan": "draft",
		} {
			if got := importedDoc(t, c, slug).Status; got != want {
				t.Errorf("%s: status = %q, want %q", slug, got, want)
			}
		}
	})

	t.Run("kinds and numbers", func(t *testing.T) {
		for slug, want := range map[string]struct {
			kind   string
			number int
		}{
			"001-forward-spec":         {"spec", 1},
			"003-record-a-decision":    {"adr", 3},
			"2026-01-01-mintable-plan": {"plan", 0},
		} {
			d := importedDoc(t, c, slug)
			if d.Kind != want.kind || d.Number != want.number {
				t.Errorf("%s = {kind:%s number:%d}, want {kind:%s number:%d}",
					slug, d.Kind, d.Number, want.kind, want.number)
			}
		}
	})

	t.Run("an accepted plan mints nothing", func(t *testing.T) {
		resp, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "proj"})
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}
		if len(resp.Tasks) != 0 {
			t.Fatalf("tasks = %+v, want none: the import bypasses the accept transaction", resp.Tasks)
		}
	})

	t.Run("an imported accepted spec has every anchor published at version 1", func(t *testing.T) {
		d := importedDoc(t, c, "001-forward-spec")
		if d.Version != 1 {
			t.Errorf("version = %d, want 1: history is not reconstructed", d.Version)
		}
		if len(d.Sections) != 2 {
			t.Fatalf("sections = %+v, want the two anchored headings", d.Sections)
		}
		for _, s := range d.Sections {
			if !s.Published {
				t.Errorf("#%s is unpublished; an accepted spec's anchors are frozen", s.Anchor)
			}
			if s.LastRevisedIn != 1 {
				t.Errorf("#%s last_revised_in = %d, want 1", s.Anchor, s.LastRevisedIn)
			}
		}
	})

	t.Run("an unresolvable reference is kept verbatim and reported", func(t *testing.T) {
		d := importedDoc(t, c, "2026-01-04-cross-corpus-plan")
		if len(d.Edges) != 1 {
			t.Fatalf("edges = %+v, want the one covers edge", d.Edges)
		}
		if d.Edges[0].ToExternal != "rdf-registry:ADR-0006" || d.Edges[0].ToDoc != 0 {
			t.Errorf("edge = %+v, want it kept in to_external", d.Edges[0])
		}
		if !strings.Contains(out, "2026-01-04-cross-corpus-plan: rdf-registry:ADR-0006") {
			t.Errorf("output = %q, want the unresolvable reference reported", out)
		}
	})

	t.Run("the task: key is recorded nowhere", func(t *testing.T) {
		d := importedDoc(t, c, "2026-01-03-task-key-plan")
		if len(d.Edges) != 0 || len(d.EdgesIn) != 0 {
			t.Errorf("edges = %+v / %+v, want none: task: is not a relation", d.Edges, d.EdgesIn)
		}
		if !strings.Contains(d.Body, "task: WL-99") {
			t.Errorf("body = %q, want the source stored verbatim", d.Body)
		}
		// The task set is asserted empty above; nothing else on the document
		// can carry a task id.
	})

	t.Run("a subdirectory of specs/ is not imported", func(t *testing.T) {
		if _, err := resolveDocID(ctx, c, "900-generated-view"); err == nil {
			t.Error("specs/inlined/ was walked; the generated view must never be imported")
		}
		resp, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "proj"})
		if err != nil {
			t.Fatalf("list docs: %v", err)
		}
		if len(resp.Docs) != 7 {
			t.Errorf("imported %d documents, want 7", len(resp.Docs))
		}
	})

	// Last: this one runs the import a second time.
	t.Run("re-running changes nothing", func(t *testing.T) {
		before, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "proj"})
		if err != nil {
			t.Fatalf("list docs: %v", err)
		}
		out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", importCorpus())
		if err != nil {
			t.Fatalf("second doc import: %v\noutput: %s", err, out)
		}
		if !strings.Contains(out, "0 created") || !strings.Contains(out, "7 already present") {
			t.Fatalf("second import summary = %q, want it to create nothing", out)
		}
		after, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: "proj"})
		if err != nil {
			t.Fatalf("list docs: %v", err)
		}
		if !reflect.DeepEqual(before.Docs, after.Docs) {
			t.Errorf("the second import changed the corpus:\nbefore %+v\nafter  %+v", before.Docs, after.Docs)
		}
	})
}

// TestDocImportDryRun: a dry run prints the would-be corpus and the references
// that will not resolve, and writes nothing.
func TestDocImportDryRun(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", importCorpus(), "--dry-run")
	if err != nil {
		t.Fatalf("doc import --dry-run: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		"001-forward-spec", "accepted", "003-record-a-decision", "adr",
		"7 document(s)", "2026-01-04-cross-corpus-plan: rdf-registry:ADR-0006",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output = %q, want it to contain %q", out, want)
		}
	}
	resp, _, err := c.ListDocs(context.Background(), cli.DocListFilter{Project: "proj"})
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	if len(resp.Docs) != 0 {
		t.Fatalf("a dry run wrote %d documents", len(resp.Docs))
	}
}
