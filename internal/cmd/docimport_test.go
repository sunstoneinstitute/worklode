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
			"2026-01-01-mintable-plan": {"plan", 1}, // 029 §4: allocated, not 0
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

// TestDocImportForwardBlockingPlanChain: a plan series whose phases each block
// the next imports whole. `blocks` is the one relation the server resolves at
// create time (025 §5), so pass 1 has to create the chain back to front
// (WL-339) — in walk order every phase would name a plan that does not exist
// yet and the whole import would fail on the first one.
func TestDocImportForwardBlockingPlanChain(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	dir := t.TempDir()
	plans := filepath.Join(dir, "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	slugs := []string{"2026-08-22-mesh-1", "2026-08-22-mesh-2", "2026-08-22-mesh-3"}
	for i, slug := range slugs {
		body := "---\nstatus: draft\n"
		if i+1 < len(slugs) {
			body += "blocks: " + slugs[i+1] + ".md\n"
		}
		body += "---\n\n# " + slug + "\n"
		if err := os.WriteFile(filepath.Join(plans, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", dir)
	if err != nil {
		t.Fatalf("doc import: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "3 created") {
		t.Fatalf("import summary = %q, want 3 created", out)
	}
	for i := 0; i+1 < len(slugs); i++ {
		d := importedDoc(t, c, slugs[i])
		if len(d.Edges) != 1 {
			t.Fatalf("%s edges = %+v, want the one blocks edge", slugs[i], d.Edges)
		}
		if want := importedDoc(t, c, slugs[i+1]).ID; d.Edges[0].ToDoc != want {
			t.Errorf("%s blocks edge = %+v, want to_doc %d", slugs[i], d.Edges[0], want)
		}
	}
}

// TestDocImportRerunUpdatesDriftedDocs is WL-357: frontmatter added to an
// already-imported file (the report's case was `requires:` on a plan) was
// silently discarded on re-run — pass 1 skipped the existing slug and pass 2
// rewired edges from the *stored* body, which never saw the edit. A re-run
// must update the drifted body where an in-place edit is legal (plans at any
// status, draft specs/ADRs) and report loudly where it is not (accepted
// specs/ADRs, which are revised, never edited).
func TestDocImportRerunUpdatesDriftedDocs(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	// A mutable copy of the fixture corpus.
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(importCorpus())); err != nil {
		t.Fatal(err)
	}
	if out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", dir); err != nil {
		t.Fatalf("first doc import: %v\noutput: %s", err, out)
	}

	// The reported bug: a plan gains a requires: edge after its import.
	planFile := filepath.Join(dir, "plans", "2026-01-04-cross-corpus-plan.md")
	if err := os.WriteFile(planFile, []byte(`---
status: draft
covers:
  - "rdf-registry:ADR-0006"
requires:
  - docs/specs/002-target-spec.md#sec-1
---

# A plan covering another corpus

Now also declaring a dependency on spec 002.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A draft spec drifts: editable in place.
	draftSpec := filepath.Join(dir, "specs", "002-target-spec.md")
	draftBody, err := os.ReadFile(draftSpec)
	if err != nil {
		t.Fatal(err)
	}
	const draftMarker = "A drifted paragraph the re-run must store."
	if err := os.WriteFile(draftSpec, append(draftBody, []byte("\n"+draftMarker+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	// An accepted spec drifts: not editable in place, must be reported.
	acceptedSpec := filepath.Join(dir, "specs", "001-forward-spec.md")
	acceptedBody, err := os.ReadFile(acceptedSpec)
	if err != nil {
		t.Fatal(err)
	}
	const acceptedMarker = "A drifted paragraph the re-run must refuse."
	if err := os.WriteFile(acceptedSpec, append(acceptedBody, []byte("\n"+acceptedMarker+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runLodeOutErr(t, "doc", "import", "--project", "proj", "--docs", dir)
	if err != nil {
		t.Fatalf("second doc import: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "0 created") || !strings.Contains(stdout, "7 already present") ||
		!strings.Contains(stdout, "2 updated") {
		t.Errorf("summary = %q, want 0 created, 7 already present, 2 updated", stdout)
	}

	t.Run("a plan's added requires edge reaches the backbone", func(t *testing.T) {
		plan := importedDoc(t, c, "2026-01-04-cross-corpus-plan")
		target := importedDoc(t, c, "002-target-spec")
		var requires []model.DocEdge
		for _, e := range plan.Edges {
			if e.Type == "requires" {
				requires = append(requires, e)
			}
		}
		if len(requires) != 1 || requires[0].ToDoc != target.ID || requires[0].ToAnchor != "sec-1" {
			t.Errorf("requires edges = %+v, want one to doc %d #sec-1", requires, target.ID)
		}
	})

	t.Run("a drifted draft spec is updated in place", func(t *testing.T) {
		if d := importedDoc(t, c, "002-target-spec"); !strings.Contains(d.Body, draftMarker) {
			t.Errorf("draft spec body was not updated; drift silently kept")
		}
	})

	t.Run("a drifted accepted spec is refused and reported", func(t *testing.T) {
		if d := importedDoc(t, c, "001-forward-spec"); strings.Contains(d.Body, acceptedMarker) {
			t.Errorf("accepted spec body was edited in place; it must be revised instead")
		}
		if !strings.Contains(stderr, "001-forward-spec") || !strings.Contains(stderr, "lode doc revise") {
			t.Errorf("stderr = %q, want the drifted accepted spec named with the revise pointer", stderr)
		}
		if !strings.Contains(stdout, "1 drifted") {
			t.Errorf("summary = %q, want the drifted count", stdout)
		}
	})

	t.Run("a further unchanged re-run updates nothing", func(t *testing.T) {
		before, _, err := c.ListDocs(context.Background(), cli.DocListFilter{Project: "proj"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", dir)
		if err != nil {
			t.Fatalf("third doc import: %v\noutput: %s", err, out)
		}
		if !strings.Contains(out, "0 updated") {
			t.Errorf("summary = %q, want 0 updated", out)
		}
		after, _, err := c.ListDocs(context.Background(), cli.DocListFilter{Project: "proj"})
		if err != nil {
			t.Fatal(err)
		}
		// The still-drifted accepted spec is reported again, but nothing moves.
		if !reflect.DeepEqual(before.Docs, after.Docs) {
			t.Errorf("the third import changed the corpus:\nbefore %+v\nafter  %+v", before.Docs, after.Docs)
		}
	})
}
