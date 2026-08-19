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
// Re-running is a no-op by *slug identity*: a slug already present in the
// project is left alone. The plan proposed a deterministic external id per
// file path, deduped through RecordEvent — client-side slug identity gives the
// same guarantee with no server change, and reads back plainly in `lode doc
// list`. The cost is that a drifted body is not updated; the corpus files are
// deleted right after the import, so there is no drift to track.
package cmd

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
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
	number int    // 0 for a plan, which carries none (025 §14.3)
	slug   string
	status string
	title  string
	body   string
	fm     *designdoc.Frontmatter
}

// unresolvedRef is one frontmatter reference no walked document satisfies.
type unresolvedRef struct{ slug, ref string }

// importSubdirs are the corpus subdirectories and the kind their files take.
// Only these two, and only their top level: docs/specs/inlined/ is a generated
// view of the same specs, and importing it would duplicate the corpus.
var importSubdirs = []struct{ sub, kind string }{{"specs", "spec"}, {"plans", "plan"}}

// importLeadingNumber extracts a spec or ADR filename's corpus number.
var importLeadingNumber = regexp.MustCompile(`^(\d+)-`)

// importBareNumber is a reference that is nothing but a corpus number, the one
// non-slug form the local resolver understands (mirroring store.resolveDocRef).
var importBareNumber = regexp.MustCompile(`^(\d+)$`)

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
stderr; that is a fact about the corpus, not an import failure.

History is not reconstructed: every imported document lands at version 1, so
last_revised_in is 1 for every section and a claim pinned to an earlier version
re-baselines at import.

Re-running is safe. A slug already present in the project is left alone, and
its edges are re-wired, so an interrupted import is finished by running it
again.

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
				return fmt.Errorf(`no project: pass --project or --repo, set current_project in .worklode/config.toml or ~/.config/worklode/config.toml, or map this repo with "lode project add-repo"`)
			}

			resp, _, err := c.ListDocs(cmd.Context(), cli.DocListFilter{Project: sc.Project})
			if err != nil {
				return fmt.Errorf("list the project's documents: %w", err)
			}
			ids := make(map[string]int64, len(docs))
			for _, d := range resp.Docs {
				ids[d.Slug] = d.ID
			}

			var created, skipped int
			for _, d := range docs {
				if _, ok := ids[d.slug]; ok {
					skipped++
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
			fmt.Fprintf(out, "imported %d document(s) into %s: %d created, %d already present, %d wired\n",
				len(docs), sc.Project, created, skipped, len(docs))
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

// unresolvedImportRefs lists the frontmatter references no walked document
// satisfies, in walk order. They are not errors: a reference across corpora
// (another repo's spec) is a real fact, and the backbone keeps it verbatim in
// to_external. Reporting them locally is what makes a dry run useful.
//
// The 025 §14.3 <KEY>-<TYPE>-<n> shorthand is deliberately not resolved here:
// it is matched against the project's key, which the walker cannot know
// without a round trip, so such a reference is reported even though the server
// would resolve it.
func unresolvedImportRefs(docs []importDoc) []unresolvedRef {
	slugs := make(map[string]bool, len(docs))
	numbered := map[int]int{}
	for _, d := range docs {
		slugs[d.slug] = true
		if d.kind != "plan" {
			numbered[d.number]++
		}
	}
	var out []unresolvedRef
	for _, d := range docs {
		for _, ref := range importRefs(d.fm) {
			base, _, _ := strings.Cut(ref, "#")
			base = strings.TrimSuffix(path.Base(base), ".md")
			if slugs[base] {
				continue
			}
			// A bare number resolves only when exactly one spec or ADR carries
			// it — a corpus can hold both a spec 25 and an ADR 25.
			if m := importBareNumber.FindStringSubmatch(base); m != nil {
				n, err := strconv.Atoi(m[1])
				if err == nil && numbered[n] == 1 {
					continue
				}
			}
			out = append(out, unresolvedRef{slug: d.slug, ref: ref})
		}
	}
	return out
}

// importRefs lists the frontmatter references one document would become edges
// from, mirroring store.frontmatterEdges: the acting-direction relations only,
// since the inverse spellings write no row.
func importRefs(fm *designdoc.Frontmatter) []string {
	if fm == nil {
		return nil
	}
	var out []string
	add := func(ref string) {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, ref)
		}
	}
	for _, entry := range fm.CoverageEntries() {
		add(entry.Spec)
	}
	for _, ref := range fm.Requires {
		add(ref)
	}
	for _, ref := range fm.Blocks {
		add(ref)
	}
	add(fm.WasDerivedFrom)
	for _, m := range []designdoc.AnchorMap{fm.Amends, fm.Replaces} {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, ref := range m[k] {
				add(ref)
			}
		}
	}
	return out
}

// printImportCorpus writes the would-be corpus: one line per document, then
// the counts a dry run is read for.
func printImportCorpus(w io.Writer, docs []importDoc) {
	var specs, adrs, plans int
	for _, d := range docs {
		number := "-"
		if d.number != 0 {
			number = strconv.Itoa(d.number)
		}
		fmt.Fprintf(w, "%-4s %4s  %-56s %-10s %s\n", d.kind, number, d.slug, d.status, d.title)
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

// printUnresolvedRefs reports every reference that will land in to_external,
// one per line, on stderr — the summary belongs on stdout, these belong where a
// caller can filter them out of it.
func printUnresolvedRefs(w io.Writer, refs []unresolvedRef) {
	for _, u := range refs {
		fmt.Fprintf(w, "%s: %s\n", u.slug, u.ref)
	}
	if len(refs) > 0 {
		fmt.Fprintf(w, "%d reference(s) resolve to no document in this project; kept verbatim\n", len(refs))
	}
}
