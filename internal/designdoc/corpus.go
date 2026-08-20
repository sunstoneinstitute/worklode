package designdoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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
	title, ok := docTitle(doc.Preamble)
	if !ok {
		return nil, CorpusDoc{}, fmt.Errorf("%s: no H1 title", name)
	}
	fmJSON, err := frontmatterJSON(doc.Frontmatter)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	return doc, CorpusDoc{
		Path: p, Filename: name,
		Status: doc.Frontmatter.Status, Title: title,
		Source: src, FrontmatterJSON: fmJSON,
	}, nil
}

// Title is the document's H1 title — the first "# …" line of the preamble.
// Reported false when the preamble carries none.
func Title(d *Document) (string, bool) {
	return docTitle(d.Preamble)
}

// docTitle returns the preamble's first "# " heading line, hash and
// whitespace stripped. ok is false when the preamble has none.
func docTitle(preamble string) (string, bool) {
	for _, line := range strings.Split(preamble, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "#")), true
		}
	}
	return "", false
}

// frontmatterJSON re-encodes the frontmatter's inner YAML as JSON, so the
// backbone can store it without a second parser (025 §16.3). YAML scalar
// timestamps (e.g. "issued: 2026-01-01") are normalized to RFC3339
// ("2026-01-01T00:00:00Z") in the process — the value is preserved, only its
// lexical form changes.
func frontmatterJSON(f *Frontmatter) (json.RawMessage, error) {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(f.inner), &m); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	return b, nil
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

// anchorRelOrder is the fixed rel order anchorEdges walks, both kinds' four
// AnchorMap fields (025 §5.1) — implements is handled separately by loadPlans,
// since it is a plan-only, document-level RefList rather than an AnchorMap.
var anchorRelOrder = []struct {
	rel string
	get func(*Frontmatter) AnchorMap
}{
	{"amends", func(f *Frontmatter) AnchorMap { return f.Amends }},
	{"amendedBy", func(f *Frontmatter) AnchorMap { return f.AmendedBy }},
	{"replaces", func(f *Frontmatter) AnchorMap { return f.Replaces }},
	{"isReplacedBy", func(f *Frontmatter) AnchorMap { return f.IsReplacedBy }},
}

// anchorEdges extracts the amends/amendedBy/replaces/isReplacedBy edges from
// fm's four AnchorMaps, in a fixed rel order with map keys sorted, so output
// is deterministic run to run.
func anchorEdges(fm *Frontmatter) []EdgeMeta {
	if fm == nil {
		return nil
	}
	var edges []EdgeMeta
	for _, r := range anchorRelOrder {
		m := r.get(fm)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			srcAnchor := anchorMapSrcAnchor(k)
			for _, ref := range m[k] {
				target, targetAnchor := SplitFragment(ref)
				edges = append(edges, EdgeMeta{
					SrcAnchor: srcAnchor, Rel: r.rel,
					Target: target, TargetAnchor: targetAnchor,
				})
			}
		}
	}
	return edges
}

// anchorMapSrcAnchor converts an AnchorMap key to a SectionMeta-shaped
// anchor: "." (document-level) is "", "#sec-3" is "sec-3".
func anchorMapSrcAnchor(key string) string {
	if key == "." {
		return ""
	}
	return strings.TrimPrefix(key, "#")
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
		for _, ref := range doc.Frontmatter.CoverageEntries() {
			base, frag := SplitFragment(ref.Spec)
			cd.Edges = append(cd.Edges, EdgeMeta{Rel: "covers", Target: base, TargetAnchor: frag})
		}
		cd.Edges = append(cd.Edges, anchorEdges(doc.Frontmatter)...)
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
	sort.Strings(names)
	return names, nil
}
