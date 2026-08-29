package designdoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SectionMeta is one anchored section, as loaded for sync (025 §16.2).
// Anchorless headings are skipped entirely — they have nothing to key a
// backbone row on.
type SectionMeta struct {
	Anchor   string // "sec-4.1a", never empty — anchorless headings are skipped
	Heading  string
	Depth    int // 2..6
	Position int // 0-based document order over the anchored sections
}

// EdgeMeta is one frontmatter-derived edge (025 §5.1): exactly the relation
// list documented there, never requires/isRequiredBy/task.
type EdgeMeta struct {
	SrcAnchor    string // "" = document-level ("." in the AnchorMap)
	Rel          string // implements | amends | amendedBy | replaces | isReplacedBy
	Target       string // the raw reference with any fragment stripped; "NO-SPEC" allowed
	TargetAnchor string // "sec-2" when the reference carried #sec-2, else ""
}

// CorpusDoc is one design document loaded for sync, with its file-derived
// identity (025 §16.3): kind and ordinal come from the filename and corpus, not
// the frontmatter, so a document's identity survives a frontmatter typo.
type CorpusDoc struct {
	Path, Filename  string
	Kind            string // "spec" | "adr" | "plan"
	Ordinal         string // "14" for spec/adr; "34-1" for plan (025 §16.3)
	Status, Title   string
	Source          []byte          // the full file, frontmatter included
	FrontmatterJSON json.RawMessage // the YAML header re-encoded as JSON
	Sections        []SectionMeta   // empty for plans (025 §9)
	Edges           []EdgeMeta
	// Number is this document's own corpus number — the <n> a <KEY>-<TYPE>-<n>
	// shorthand or a bare-number reference names it by (026 §3-4) — 0 when
	// unknown. Loaded from a spec/ADR's filename (both loaders can read it: it
	// leads the filename); a plan loaded from local files carries 0, since 029
	// §4's plan sequence is a backbone fact no plan file records anywhere. A
	// plan loaded from the backbone (CorpusDocFromBody) carries its real one.
	Number int
}

// LoadSyncCorpus loads specDir as SPEC/ADR documents and planDir as PLAN
// documents; either may be "" (that corpus is not configured). Results are
// spec-corpus documents (specs and ADRs interleaved) in filename order,
// followed by plan-corpus documents in filename order.
func LoadSyncCorpus(specDir, planDir string) ([]CorpusDoc, error) {
	var out []CorpusDoc
	if specDir != "" {
		files, err := corpusFilenames(specDir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			d, err := loadSpecOrADR(specDir, f)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	if planDir != "" {
		plans, err := loadPlans(planDir)
		if err != nil {
			return nil, err
		}
		out = append(out, plans...)
	}
	return out, nil
}

// loadDoc parses one file and derives the kind-independent fields: status,
// title, FrontmatterJSON. Frontmatter absence, a parse failure, an empty
// status, or a missing H1 are sync errors naming the file (025 §16.2).
func loadDoc(dir, name string) (*Document, CorpusDoc, error) {
	p := filepath.Join(dir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("read %s: %w", p, err)
	}
	doc, cd, err := docFromSource(name, src)
	if err != nil {
		return nil, CorpusDoc{}, err
	}
	cd.Path = p
	return doc, cd, nil
}

// docFromSource derives the kind-independent fields from a document's bytes,
// wherever they came from: status, title, FrontmatterJSON. name is used only
// to name the document in an error. Path is left to the caller, which is the
// one thing a file and a backbone row disagree about.
func docFromSource(name string, src []byte) (*Document, CorpusDoc, error) {
	doc, err := Parse(src)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	if doc.Frontmatter == nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: no frontmatter", name)
	}
	if doc.Frontmatter.Status == "" {
		return nil, CorpusDoc{}, fmt.Errorf("%s: frontmatter has no status", name)
	}
	title, ok := Title(doc)
	if !ok {
		return nil, CorpusDoc{}, fmt.Errorf("%s: no H1 title", name)
	}
	fmJSON, err := doc.Frontmatter.jsonBytes()
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	return doc, CorpusDoc{
		Filename: name,
		Status:   doc.Frontmatter.Status, Title: title,
		Source: src, FrontmatterJSON: fmJSON,
	}, nil
}

// CorpusPath is the corpus path a document of the given kind and slug is
// written at (025 §16.1), and so the form every covers/requires reference in
// the corpus names it by. It is the bridge for a caller reading documents
// from the backbone rather than from disk: the backbone stores kind and slug,
// while the references between documents are still written as paths.
func CorpusPath(kind, slug string) string {
	return path.Join(CorpusDir(kind), slug+".md")
}

// CorpusDir is the corpus directory documents of the given kind live in
// (025 §16.1): specs and ADRs share one, plans have their own.
func CorpusDir(kind string) string {
	if kind == "plan" {
		return planCanonDefault
	}
	return specCanonDefault
}

// CorpusDocFromBody builds a CorpusDoc from a document body held in memory —
// the copy the backbone serves — rather than from a file. docPath is the
// corpus path its references are written against (see CorpusPath), kind is
// "spec", "adr" or "plan", and number is the document's own backbone number
// (model.Doc.Number) — the one fact CorpusDocFromBody cannot recover from
// docPath or body alone, since a shorthand-form reference needs it and
// neither the corpus path nor the frontmatter carries it (WL-409).
//
// It derives everything LoadSyncCorpus does except Ordinal, which is a
// corpus-position fact no single document carries: the backbone assigns
// document identity itself, so nothing reading from it needs one.
func CorpusDocFromBody(docPath, kind string, number int, body []byte) (CorpusDoc, error) {
	name := path.Base(docPath)
	doc, cd, err := docFromSource(name, body)
	if err != nil {
		return CorpusDoc{}, err
	}
	cd.Path, cd.Kind, cd.Number = docPath, kind, number
	if kind == "plan" {
		// Sections deliberately unset: plans carry none (025 §9).
		cd.Edges = append(cd.Edges, planEdges(doc.Frontmatter)...)
		return cd, nil
	}
	sections, err := sectionMetas(doc, name)
	if err != nil {
		return CorpusDoc{}, err
	}
	cd.Sections = sections
	cd.Edges = anchorEdges(doc.Frontmatter)
	return cd, nil
}

// Title is the document's H1 title — the preamble's first "# …" line, hash
// and whitespace stripped. Reported false when the preamble carries none.
func Title(d *Document) (string, bool) {
	for line := range strings.SplitSeq(d.Preamble, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "#")), true
		}
	}
	return "", false
}

// loadSpecOrADR loads one SPEC-corpus document: kind from frontmatter (adr
// vs spec), ordinal from the filename's leading number (a spec-corpus file
// without one is an error), sections and edges from the parsed document.
func loadSpecOrADR(dir, name string) (CorpusDoc, error) {
	doc, cd, err := loadDoc(dir, name)
	if err != nil {
		return CorpusDoc{}, err
	}
	if doc.Frontmatter.Kind == "adr" {
		cd.Kind = "adr"
	} else {
		cd.Kind = "spec"
	}
	n, ok := leadingNumber(name)
	if !ok {
		return CorpusDoc{}, fmt.Errorf("%s: no leading number", name)
	}
	cd.Ordinal = strconv.Itoa(n)
	cd.Number = n
	sections, err := sectionMetas(doc, name)
	if err != nil {
		return CorpusDoc{}, err
	}
	cd.Sections = sections
	cd.Edges = anchorEdges(doc.Frontmatter)
	return cd, nil
}

// sectionMetas converts a document's anchored sections to SectionMeta, in
// document order. A duplicate anchor within one document is a sync error
// naming the file — an ambiguous key downstream is worse than a rejected
// document.
func sectionMetas(doc *Document, name string) ([]SectionMeta, error) {
	var out []SectionMeta
	seen := make(map[string]bool)
	for _, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		if seen[sec.Anchor] {
			return nil, fmt.Errorf("%s: duplicate anchor %q", name, sec.Anchor)
		}
		seen[sec.Anchor] = true
		out = append(out, SectionMeta{
			Anchor:   sec.Anchor,
			Heading:  sec.Title,
			Depth:    sec.Level,
			Position: len(out),
		})
	}
	return out, nil
}

// anchorRels is the rel set anchorEdges keeps: both kinds' four AnchorMap
// fields (025 §5.1). Unlike the store, the corpus records both directions —
// it mirrors the corpus as authored, so an `amendedBy:` a document declares
// about itself is a fact of that document.
var anchorRels = []string{"amends", "amendedBy", "replaces", "isReplacedBy"}

// planEdges is a plan's edges: its coverage assertions — the retired
// `implements` spelling read as `covers` (026 §5.1) — then its defers
// handoffs (026 §5.3), then the anchor relations every document kind can
// carry. A defers entry projects the same way a covers entry does — the
// section it names becomes the edge's target anchor — but carries no owner:
// EdgeMeta has no field for it, the same deliberate omission as covers
// carrying no coverage level. The owner lives in the backbone's
// doc_coverage_completed_with, not the sync-projected corpus.
func planEdges(fm *Frontmatter) []EdgeMeta {
	return edgeMetas(fm.RefsFor(append([]string{"covers", "defers"}, anchorRels...)...))
}

// anchorEdges extracts the amends/amendedBy/replaces/isReplacedBy edges from
// fm's four AnchorMaps. Frontmatter.Refs fixes the order, so output is
// deterministic run to run.
func anchorEdges(fm *Frontmatter) []EdgeMeta {
	return edgeMetas(fm.RefsFor(anchorRels...))
}

// edgeMetas turns frontmatter references into corpus edges, splitting each
// ref's "#sec-…" fragment off as the target anchor.
func edgeMetas(refs []Ref) []EdgeMeta {
	var edges []EdgeMeta
	for _, r := range refs {
		target, targetAnchor := SplitFragment(r.Ref)
		edges = append(edges, EdgeMeta{
			SrcAnchor: r.SrcAnchor, Rel: r.Rel,
			Target: target, TargetAnchor: targetAnchor,
		})
	}
	return edges
}

// loadPlans loads planDir's documents as CorpusDocs, in two passes: the
// first parses each file and derives its spec ordinal (from `implements`);
// the second numbers plan ordinals within each spec-ordinal group, ascending
// by filename (025 §16.3). Plans carry no Sections (025 §9).
func loadPlans(planDir string) ([]CorpusDoc, error) {
	files, err := corpusFilenames(planDir) // already sorted ascending
	if err != nil {
		return nil, err
	}
	type pending struct {
		doc     CorpusDoc
		specOrd int
	}
	var plans []pending
	for _, f := range files {
		doc, cd, err := loadDoc(planDir, f) // doc.Sections deliberately unused: plans carry none (025 §9)
		if err != nil {
			return nil, err
		}
		cd.Kind = "plan"
		specOrd, err := planSpecOrdinal(doc.Frontmatter, f)
		if err != nil {
			return nil, err
		}
		cd.Edges = append(cd.Edges, planEdges(doc.Frontmatter)...)
		plans = append(plans, pending{doc: cd, specOrd: specOrd})
	}
	// Second pass: number within each spec-ordinal group, ascending filename
	// (files is sorted, so arrival order is corpus order — 025 §16.3).
	counts := map[int]int{}
	var out []CorpusDoc
	for _, p := range plans {
		counts[p.specOrd]++
		p.doc.Ordinal = fmt.Sprintf("%d-%d", p.specOrd, counts[p.specOrd])
		out = append(out, p.doc)
	}
	return out, nil
}

// planSpecOrdinal derives the plan id's spec ordinal from the first
// coverage entry (025 §16.3): NO-SPEC or an absent key → 0.
func planSpecOrdinal(fm *Frontmatter, name string) (int, error) {
	entries := fm.CoverageEntries()
	if len(entries) == 0 {
		return 0, nil
	}
	base, _ := SplitFragment(entries[0].Spec)
	if base == "NO-SPEC" {
		return 0, nil
	}
	n, ok := leadingNumber(path.Base(base))
	if !ok {
		return 0, fmt.Errorf("%s: covers %q has no leading spec number to derive the plan id from (025 §16.3)", name, base)
	}
	return n, nil
}

// leadingNumberPattern extracts a corpus filename's leading document number
// ("014-design-documents...md" -> "014").
var leadingNumberPattern = regexp.MustCompile(`^(\d+)-`)

// leadingNumber parses a corpus filename's leading document number, ignoring
// any zero-padding — "0140-decoy.md" is 140, distinct from "014-x.md"'s 14.
func leadingNumber(filename string) (int, bool) {
	m := leadingNumberPattern.FindStringSubmatch(filename)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// corpusFilenames lists a corpus directory's document filenames (*.md),
// sorted so a load is deterministic.
func corpusFilenames(corpusDir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(corpusDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("list corpus %s: %w", corpusDir, err)
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	slices.Sort(names)
	return names, nil
}
