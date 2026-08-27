// docimport.go is `lode doc import`: the one-way cutover that moves a git
// corpus of design documents into the backbone (025 §12). It is a client-side
// walker over internal/designdoc writing through the public API only — the
// same re-runnable-backfill shape as `lode inbox import` — so the server keeps
// one create path and one edge-resolution path.
//
// Import runs in two passes because the corpus references forward as well as
// backward: a spec amends a spec written after it, a plan covers a spec it
// precedes in the walk. Pass 1 creates every document (its edges resolving
// against whatever exists at that moment, the rest landing in to_external);
// pass 2 asks the server to re-resolve every document's frontmatter now that
// the whole corpus is present.
//
// One relation cannot wait for pass 2: the server resolves a `blocks` edge at
// create time and refuses one naming no plan (025 §5), so pass 1 creates the
// corpus in an order that puts an ordering edge's target first
// (importCreateOrder).
//
// Re-running matches by *slug identity*: a slug already present in the
// project is compared against the walked file, and an identical body is left
// alone. A drifted body is updated in place where an in-place edit is legal —
// plans at any status, draft specs and ADRs — because a corpus that keeps its
// files (WL-357: edge-agent) edits frontmatter there and re-runs the import
// expecting the backbone to follow. An accepted spec or ADR cannot be edited
// in place (025 §7: revise it), so its drift is reported loudly instead of
// silently keeping the stored body while claiming the document "wired".
package cmd

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// importDoc is one walked corpus file, parsed and ready to create.
type importDoc struct {
	path   string // the file, for error messages
	kind   string // spec | adr | plan
	number int    // a plan's filename carries none, so this stays 0 (auto-assigned)
	slug   string
	status string
	title  string
	body   string
	fm     *designdoc.Frontmatter
}

// unresolvedRef is one frontmatter reference no walked document satisfies.
type unresolvedRef struct{ slug, ref string }

// driftedDoc is one walked file whose body differs from the stored document
// and cannot be edited in place (an accepted or superseded spec/ADR).
type driftedDoc struct{ path, kind, status string }

// noSpecSentinel is 026 §4.3's "no governing spec" coverage declaration. It
// resolves to nothing on purpose, so it is reported apart from the references
// that were meant to resolve and did not.
const noSpecSentinel = "NO-SPEC"

// importSubdirs are the corpus subdirectories and the kind their files take.
// Only these two, and only their top level: docs/specs/inlined/ is a generated
// view of the same specs, and importing it would duplicate the corpus.
var importSubdirs = []struct{ sub, kind string }{{"specs", "spec"}, {"plans", "plan"}}

// importLeadingNumber extracts a spec or ADR filename's corpus number.
var importLeadingNumber = regexp.MustCompile(`^(\d+)-`)

func newDocImportCmd() *cobra.Command {
	var scope scopeFlags
	var docsDir string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a git corpus of design documents into the backbone",
		Long: `Import a git corpus of design documents into the backbone.

Walks <docs>/specs/*.md and <docs>/plans/*.md — the top level of each, never a
subdirectory — and creates one document per file, keeping the frontmatter's
status verbatim. A plan with no status is imported accepted: the corpus's spent
plans predate the status key, and importing them as draft would list shipped
work as pending review. Importing an accepted plan does not mint tasks, because
the import states the status directly instead of going through the accept gate.

Edges are wired in a second pass, once every document exists, because the
corpus references forward as well as backward. A reference no document in the
project resolves to is kept verbatim as an external reference and reported on
stderr; that is a fact about the corpus, not an import failure. The exception
is blocks/blockedBy, which the server resolves at create time: documents
are created in an order that satisfies those references first, and one still
unresolvable then is an error.

History is not reconstructed: every imported document lands at version 1, so
last_revised_in is 1 for every section and a claim pinned to an earlier version
re-baselines at import.

Re-running is safe. A slug already present with an identical body is left
alone, and its edges are re-wired, so an interrupted import is finished by
running it again. A body that drifted from the backbone is updated in place
where an in-place edit is legal (a plan at any status, a draft spec or ADR);
a drifted accepted spec or ADR is reported on stderr instead — revise it with
lode doc revise. A dry run is entirely local and cannot see drift.

Stating a status needs the admin-only doc.import permission.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The whole corpus is parsed and locally resolved before anything
			// is written: a corpus that is half imported is worse than one that
			// is not imported at all.
			docs, err := walkImportCorpus(docsDir)
			if err != nil {
				return err
			}
			unresolved := unresolvedImportRefs(docs)

			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			// A dry run is entirely local: no client is built, so it reports on
			// a corpus with no server configured and cannot write by accident.
			if dryRun {
				printImportCorpus(out, docs)
				printUnresolvedRefs(errOut, unresolved)
				return nil
			}

			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errNoProject
			}

			resp, _, err := c.ListDocs(cmd.Context(), cli.DocListFilter{Project: sc.Project})
			if err != nil {
				return fmt.Errorf("list the project's documents: %w", err)
			}
			ids := make(map[string]int64, len(docs))
			for _, d := range resp.Docs {
				ids[d.Slug] = d.ID
			}

			var created, present, updated int
			var drifted []driftedDoc
			for _, d := range importCreateOrder(docs) {
				if id, ok := ids[d.slug]; ok {
					present++
					stored, _, err := c.GetDoc(cmd.Context(), id)
					if err != nil {
						return fmt.Errorf("read the stored body of %s: %w", d.path, err)
					}
					if stored.Body == d.body {
						continue
					}
					// Editable in place: a plan at any status, a draft spec or
					// ADR (025 §7/§9 — the same gate UpdateDocBody enforces,
					// checked here to tell "cannot" from a real failure).
					if stored.Kind != "plan" && stored.Status != "draft" {
						drifted = append(drifted, driftedDoc{
							path: d.path, kind: stored.Kind, status: stored.Status,
						})
						continue
					}
					if _, _, err := c.UpdateDocBody(cmd.Context(), id, d.body); err != nil {
						return fmt.Errorf("update the drifted body of %s: %w", d.path, err)
					}
					updated++
					continue
				}
				nd, _, err := c.CreateDoc(cmd.Context(), model.CreateDocInput{
					Project: sc.Project, Kind: d.kind, Number: d.number,
					Slug: d.slug, Body: d.body, Status: d.status,
				})
				if err != nil {
					return fmt.Errorf("create %s: %w", d.path, err)
				}
				ids[d.slug] = nd.ID
				created++
			}
			// Pass 2 covers the skipped documents too: an earlier run that died
			// between the passes left their references unresolved.
			for _, d := range docs {
				if _, _, err := c.ReplaceDocEdges(cmd.Context(), ids[d.slug]); err != nil {
					return fmt.Errorf("wire the edges of %s: %w", d.path, err)
				}
			}
			summary := fmt.Sprintf("imported %d document(s) into %s: %d created, %d already present, %d updated, %d wired",
				len(docs), sc.Project, created, present, updated, len(docs))
			if len(drifted) > 0 {
				summary += fmt.Sprintf(", %d drifted (not updated)", len(drifted))
			}
			fmt.Fprintln(out, summary)
			printDriftedDocs(errOut, drifted)
			printUnresolvedRefs(errOut, unresolved)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&docsDir, "docs", "docs", "corpus root holding specs/ and plans/")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the would-be corpus and every unresolvable reference; write nothing")
	return cmd
}

// walkImportCorpus reads every markdown file at the top level of
// <docsDir>/specs and <docsDir>/plans, in filename order, and derives what the
// backbone needs from each. A file that does not parse, or a spec-corpus file
// with no leading corpus number, aborts the walk naming the file.
func walkImportCorpus(docsDir string) ([]importDoc, error) {
	var out []importDoc
	for _, sub := range importSubdirs {
		dir := filepath.Join(docsDir, sub.sub)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			// Directories are skipped rather than descended into: the corpus is
			// flat, and docs/specs/inlined/ is a generated view of it.
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			d, err := readImportDoc(filepath.Join(dir, entry.Name()), sub.kind)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no documents under %s: expected markdown files in specs/ or plans/", docsDir)
	}
	return out, nil
}

// readImportDoc parses one corpus file. defaultKind is the kind its directory
// implies; a spec-directory file declaring `kind: adr` is an ADR (026 §4.2).
func readImportDoc(file, defaultKind string) (importDoc, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return importDoc{}, err
	}
	doc, err := designdoc.Parse(src)
	if err != nil {
		return importDoc{}, fmt.Errorf("parse %s: %w", file, err)
	}
	name := filepath.Base(file)
	d := importDoc{
		path: file,
		kind: defaultKind,
		slug: strings.TrimSuffix(name, ".md"),
		body: string(src),
		fm:   doc.Frontmatter,
	}
	if d.fm != nil && d.fm.Kind == "adr" && defaultKind == "spec" {
		d.kind = "adr"
	}
	if d.kind != "plan" {
		m := importLeadingNumber.FindStringSubmatch(name)
		if m == nil {
			return importDoc{}, fmt.Errorf(
				"%s has no leading corpus number: a %s is identified by its number (025 §14.3)", file, d.kind)
		}
		// The regexp matched digits, so the conversion cannot fail.
		d.number, _ = strconv.Atoi(m[1])
	}
	d.status = importStatus(d.kind, d.fm)
	if title, ok := designdoc.Title(doc); ok {
		d.title = title
	} else {
		d.title = d.slug
	}
	return d, nil
}

// importStatus is the status a corpus file is imported at: its frontmatter's,
// verbatim, and otherwise a default per kind.
//
// A plan with no status is accepted. The corpus's executed plans predate the
// status key, and importing them as draft would list shipped work as pending
// review; their task sets stay empty, and an empty set is not pending work. A
// spec or ADR with no status is a draft: it has been through no gate.
func importStatus(kind string, fm *designdoc.Frontmatter) string {
	if fm != nil {
		if s := strings.TrimSpace(fm.Status); s != "" {
			return s
		}
	}
	if kind == "plan" {
		return "accepted"
	}
	return "draft"
}

// importIndex resolves a frontmatter reference to the walked document it
// names, the way the server resolves one within a project: by slug, or by a
// bare corpus number when exactly one spec or ADR carries it — a corpus can
// hold both a spec 25 and an ADR 25.
//
// The 025 §14.3 <KEY>-<TYPE>-<n> shorthand is deliberately not resolved here:
// it is matched against the project's key, which the walker cannot know
// without a round trip.
type importIndex struct {
	bySlug   map[string]int
	byNumber map[int][]int
}

func newImportIndex(docs []importDoc) importIndex {
	ix := importIndex{bySlug: make(map[string]int, len(docs)), byNumber: map[int][]int{}}
	for i, d := range docs {
		ix.bySlug[d.slug] = i
		if d.kind != "plan" {
			ix.byNumber[d.number] = append(ix.byNumber[d.number], i)
		}
	}
	return ix
}

// lookup returns the position in the walk of the document a reference names.
func (ix importIndex) lookup(ref string) (int, bool) {
	base, _, _ := strings.Cut(ref, "#")
	base = strings.TrimSuffix(path.Base(base), ".md")
	if i, ok := ix.bySlug[base]; ok {
		return i, true
	}
	if n, err := strconv.Atoi(base); err == nil && len(ix.byNumber[n]) == 1 {
		return ix.byNumber[n][0], true
	}
	return 0, false
}

// importCreateOrder returns the corpus in the order pass 1 must create it: a
// document naming another in `blocks:` or `blockedBy:` comes after the one it
// names.
//
// Every other reference can wait for pass 2, which re-resolves the whole
// frontmatter once the corpus is present. An ordering edge cannot: the server
// resolves it at create time and refuses one naming no plan (025 §5), because
// a to_external ordering edge would gate nothing while looking like it did.
// Walk order is filename order, so a plan series whose phases each block the
// next fails outright unless the phases are created back to front (WL-339).
//
// A stable Kahn walk, so a corpus declaring no ordering keeps walk order. A
// reference the corpus does not satisfy constrains nothing — it resolves
// against documents already in the project, or it is reported unresolvable.
// Documents still held when nothing more can be emitted are appended in walk
// order: that is a reference cycle, and the server is the one that names it.
func importCreateOrder(docs []importDoc) []importDoc {
	ix := newImportIndex(docs)
	needs := make([]map[int]bool, len(docs))
	waiters := make([][]int, len(docs))
	for i, d := range docs {
		needs[i] = map[int]bool{}
		for _, r := range d.fm.RefsFor("blocks", "blockedBy") {
			j, ok := ix.lookup(r.Ref)
			// A self-reference is refused by the server either way; holding
			// it here would only strand the rest of the corpus behind it.
			if !ok || j == i || needs[i][j] {
				continue
			}
			needs[i][j] = true
			waiters[j] = append(waiters[j], i)
		}
	}
	out := make([]importDoc, 0, len(docs))
	done := make([]bool, len(docs))
	for progress := true; progress; {
		progress = false
		for i := range docs {
			if done[i] || len(needs[i]) > 0 {
				continue
			}
			done[i] = true
			out = append(out, docs[i])
			for _, w := range waiters[i] {
				delete(needs[w], i)
			}
			progress = true
		}
	}
	for i := range docs {
		if !done[i] {
			out = append(out, docs[i])
		}
	}
	return out
}

// unresolvedImportRefs lists the frontmatter references no walked document
// satisfies, in walk order. They are not errors: a reference across corpora
// (another repo's spec) is a real fact, and the backbone keeps it verbatim in
// to_external. Reporting them locally is what makes a dry run useful.
func unresolvedImportRefs(docs []importDoc) []unresolvedRef {
	ix := newImportIndex(docs)
	var out []unresolvedRef
	for _, d := range docs {
		for _, ref := range importRefs(d.fm) {
			if _, ok := ix.lookup(ref); !ok {
				out = append(out, unresolvedRef{slug: d.slug, ref: ref})
			}
		}
	}
	return out
}

// importRefs lists the frontmatter references one document would become edges
// from: the same walk and the same rel set the server records
// (designdoc.StoredRels), so the dry run reports what the import will write.
func importRefs(fm *designdoc.Frontmatter) []string {
	refs := fm.RefsFor(designdoc.StoredRels...)
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Ref
	}
	return out
}

// printImportCorpus writes the would-be corpus: one line per document, then
// the counts a dry run is read for.
func printImportCorpus(w io.Writer, docs []importDoc) {
	var specs, adrs, plans int
	for _, d := range docs {
		fmt.Fprintf(w, "%-4s %4s  %-56s %-10s %s\n",
			d.kind, cli.DocNumber(d.number), d.slug, d.status, d.title)
		switch d.kind {
		case "spec":
			specs++
		case "adr":
			adrs++
		default:
			plans++
		}
	}
	fmt.Fprintf(w, "\n%d document(s): %d spec(s), %d ADR(s), %d plan(s)\n", len(docs), specs, adrs, plans)
}

// printDriftedDocs names every file whose body drifted from a document the
// import cannot edit in place. Silence here was WL-357: the drifted file's
// frontmatter — its edges included — is not in the backbone, and a summary
// still counting the document as wired hid exactly that.
func printDriftedDocs(w io.Writer, drifted []driftedDoc) {
	for _, d := range drifted {
		fmt.Fprintf(w, "%s: differs from the stored body of the %s %s; not updated — revise it with lode doc revise, or align the file\n",
			d.path, d.status, d.kind)
	}
	if len(drifted) > 0 {
		fmt.Fprintf(w, "%d drifted document(s) not updated: their frontmatter, edges included, is not what the backbone holds\n",
			len(drifted))
	}
}

// printUnresolvedRefs reports every reference that will land in to_external,
// one per line, on stderr — the summary belongs on stdout, these belong where a
// caller can filter them out of it.
//
// 026 §4.3's NO-SPEC sentinel is counted apart from the rest. It also lands in
// to_external, but it names no target by design — it is a plan asserting that
// no spec governs it — so counting it with the genuinely dangling references
// would make a clean corpus look defective at exactly the moment the count is
// being read as a go/no-go.
func printUnresolvedRefs(w io.Writer, refs []unresolvedRef) {
	var dangling, sentinels int
	for _, u := range refs {
		if u.ref == noSpecSentinel {
			sentinels++
			continue
		}
		dangling++
		fmt.Fprintf(w, "%s: %s\n", u.slug, u.ref)
	}
	if dangling > 0 {
		fmt.Fprintf(w, "%d reference(s) resolve to no document in this project; kept verbatim\n", dangling)
	}
	if sentinels > 0 {
		fmt.Fprintf(w, "%d plan(s) declare %s; kept verbatim, no target expected (026 §4.3)\n",
			sentinels, noSpecSentinel)
	}
}
