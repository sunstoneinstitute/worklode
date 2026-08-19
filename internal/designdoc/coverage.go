package designdoc

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PlanningOutcome is where one spec section sits, per 026 §2.1, relative to
// the corpus's discharging-plan coverage.
type PlanningOutcome string

const (
	Full      PlanningOutcome = "full"
	Partial   PlanningOutcome = "partial"
	BoundOnly PlanningOutcome = "boundOnly"
	Unplanned PlanningOutcome = "unplanned"
)

// specCanonDefault and planCanonDefault are the conventional corpus
// directories (025 §16.1), used as the canonical key when the loaded corpus
// cannot be expressed relative to a repo root.
const (
	specCanonDefault = "docs/specs"
	planCanonDefault = "docs/plans"
)

// canonDirs derives the canonical corpus-relative prefixes every claim and
// every lookup is keyed on, from the directories the corpus was actually
// loaded from. A repo that relocates its corpus with `spec_corpus` /
// `plan_corpus` (025 §16.1) writes its references against the relocated
// directory, so keying on a hardcoded "docs/specs" would normalise a document
// and the claim naming it onto different keys and match nothing — a section
// with a full covering plan would report as unplanned, under a path that does
// not exist.
func canonDirs(specDir, planDir string) (specCanon, planCanon string) {
	return canonDir(specDir, specCanonDefault), canonDir(planDir, planCanonDefault)
}

// findRepoRoot walks up from dir to the nearest directory holding a
// ".worklode" directory — the repo root a corpus path is relative to (025
// §16.1). "" when there is none. Unexported: canonDir is the only caller,
// and WL-147 retired the exported filesystem resolver this used to belong to.
func findRepoRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(d, ".worklode")); err == nil && st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// canonDir is one corpus directory in canonical repo-relative form: an
// already-relative directory is its own, an absolute one is taken relative to
// the repo root it sits under. No directory loaded, no repo root, or a
// directory outside it falls back to the conventional layout.
func canonDir(dir, fallback string) string {
	if dir == "" {
		return fallback
	}
	if !filepath.IsAbs(dir) {
		return path.Clean(filepath.ToSlash(dir))
	}
	root := findRepoRoot(dir)
	if root == "" {
		return fallback
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fallback
	}
	return filepath.ToSlash(rel)
}

// CoveringPlan is one plan's claim on a section. A `none` claim never
// appears here: it discharges nothing and is owed no work (026 §2.1, §2.4).
type CoveringPlan struct {
	Path   string // repo-relative
	Status string // "accepted" | "superseded" | "draft"
	Level  string // "full" | "partial"
}

// claim is one plan's coverage assertion against one spec section, resolved
// once at index build time so Section need not re-parse frontmatter per
// query.
type claim struct {
	plan             string // repo-relative
	status           string // the plan's frontmatter status
	level            string // "full" | "partial" | "none"
	fullCoverageWith []string
}

// discharges reports whether status is in 026 §2.1's discharging set for
// coverage purposes: not draft. A superseded plan is spent (025 §9: accepted,
// then executed) and discharges what it covered exactly as an accepted plan
// does; the two statuses differ only in what §2.4 still owes afterwards.
func discharges(status string) bool {
	return status == "accepted" || status == "superseded"
}

// sectionKey identifies a spec section by its repo-relative spec path (§4
// reference, fragment split off) and bare anchor, both fully resolved
// (026 §5.1) — never the raw string a document happened to write.
type sectionKey struct {
	spec, anchor string
}

// PlanIndex is the plan corpus indexed for 026 §2.1 coverage queries: every
// plan's coverage claim against every section it names, keyed for lookup by
// section.
type PlanIndex struct {
	claims map[sectionKey][]claim
	status map[string]string // repo-relative plan path -> frontmatter status

	// specDir and planDir are the two corpus directories exactly as loaded
	// (CorpusDoc.Path's directory for any doc of that kind) — absolute when
	// the caller reached LoadSyncCorpus through FindCorpus, repo-relative
	// otherwise. They let resolveDoc recognise an absolute CorpusDoc.Path
	// for what it is, without ever scanning a path for a coincidental
	// substring (026 review round 2, R2-1/R2-3). "" when no doc of that
	// kind was loaded.
	specDir, planDir string

	// specCanon and planCanon are those same two corpora in the canonical
	// repo-relative form every claim is keyed by — derived from specDir and
	// planDir rather than assumed, so a relocated corpus (025 §16.1) keys
	// documents and the claims naming them the same way.
	specCanon, planCanon string
}

// NewPlanIndex indexes docs for Section queries: every plan document's
// coverage claims, plus (from any spec/ADR docs present) the spec-corpus
// directory a bare or absolute specPath resolves against. Non-plan documents
// otherwise contribute nothing — this task's predicate only walks plan-side
// `covers` claims.
func NewPlanIndex(docs []CorpusDoc) *PlanIndex {
	ix := &PlanIndex{
		claims: make(map[sectionKey][]claim),
		status: make(map[string]string),
	}
	ix.specDir, ix.planDir = corpusDirs(docs)
	ix.specCanon, ix.planCanon = canonDirs(ix.specDir, ix.planDir)
	for _, d := range docs {
		if d.Kind != "plan" {
			continue
		}
		plan := resolveDoc(d.Path, ix.planCanon, ix.planDir)
		ix.status[plan] = d.Status
		home := path.Dir(plan)
		for _, entry := range planCoverageEntries(d) {
			rawTarget, anchor := SplitFragment(entry.Spec)
			if anchor == "" || rawTarget == "NO-SPEC" {
				// A whole-document covers names no section a coverage query
				// can use, and NO-SPEC has no sections to cover (026 §2.1,
				// §4.3) — neither contributes to any section's index entry.
				continue
			}
			key := sectionKey{spec: normalizeRef(rawTarget, home), anchor: anchor}
			ix.claims[key] = append(ix.claims[key], claim{
				plan:             plan,
				status:           d.Status,
				level:            entry.Coverage,
				fullCoverageWith: normalizeList(entry.FullCoverageWith, home),
			})
		}
	}
	return ix
}

// planCoverageEntries recovers d's per-section coverage levels and
// fullCoverageWith lists. CorpusDoc.Edges does not carry them — EdgeMeta is
// exactly 025 §16.2's sync-projected relation shape — so this re-parses the
// frontmatter already captured in d.Source, using CoverageEntries to fold in
// the retired `implements` spelling (026 §5.1). d.Source is required: a
// CorpusDoc built without it (rather than through LoadSyncCorpus or with
// Source set by hand) reads as carrying no claims, silently — never
// hand-construct one for indexing without also setting Source.
func planCoverageEntries(d CorpusDoc) CoverageList {
	doc, err := Parse(d.Source)
	if err != nil || doc.Frontmatter == nil {
		return nil
	}
	return doc.Frontmatter.CoverageEntries()
}

// resolveDoc canonicalises ref — a bare filename, an already-canonical
// corpus-relative path, or an absolute CorpusDoc.Path — to the
// corpus-relative form (canon-rooted) every claim is keyed by. dir is that
// corpus's directory exactly as loaded (possibly absolute; "" if none of
// that kind was loaded).
//
// Three cases, each an exact structural match rather than a substring
// search anywhere in ref, so a repo root that itself happens to contain
// "docs/specs" or "docs/plans" elsewhere in its own path cannot
// mis-normalise a reference (026 review round 2, R2-3):
//
//  1. No "/" at all: a bare filename is always canon-relative — the
//     directory a document of this kind lives in, whatever the corpus root
//     turns out to be (026 review round 2, R2-1). This needs no comparison
//     against dir at all.
//  2. Already starts with canon+"/": left unchanged, checked by an exact
//     prefix at position 0 — never a scan of the rest of the string. Tried
//     again with a leading "/" stripped if the first attempt (and case 3)
//     fail — §4 makes that "/" optional on a repo-relative reference — but
//     only as a fallback, so an absolute CorpusDoc.Path (which also starts
//     with "/", as a real filesystem root, not a §4 reference) resolves on
//     its unmodified form first (026 review round 3, R3-1).
//  3. Otherwise: recognised only if ref sits under dir at a real directory
//     boundary (underDir), and rewritten onto canon.
//
// A ref matching none of these is returned unchanged — genuinely correct
// only for case 2's already-canonical form; here it is a plain miss this
// predicate does not diagnose (corpus validation is a later task's job).
func resolveDoc(ref, canon, dir string) string {
	if ref == "" {
		return ref
	}
	if !strings.Contains(ref, "/") {
		return canon + "/" + ref
	}
	if resolved, ok := resolveDocOnce(ref, canon, dir); ok {
		return resolved
	}
	// §4: a repo-relative reference's leading "/" is optional —
	// "docs/specs/x.md" and "/docs/specs/x.md" are the same reference. Only
	// retried here, after ref failed to resolve as written: an absolute
	// CorpusDoc.Path also starts with "/", but names a real filesystem
	// location that underDir needs intact, so it must get first try
	// unmodified rather than have that "/" stripped on the assumption it is
	// a §4 reference.
	resolved, ok := resolveDocOnce(strings.TrimPrefix(ref, "/"), canon, dir)
	if !ok {
		return ref
	}
	return resolved
}

// resolveDocOnce is resolveDoc's canon-prefix/underDir attempt, tried twice
// (verbatim, then with a leading "/" stripped) so the two meanings of a
// leading "/" — an OS filesystem root and §4's optional repo-relative
// marker — never collide.
func resolveDocOnce(ref, canon, dir string) (string, bool) {
	if strings.HasPrefix(ref, canon+"/") {
		return ref, true
	}
	if rel, ok := underDir(ref, dir); ok {
		return path.Join(canon, rel), true
	}
	return "", false
}

// underDir reports whether p is dir or a proper descendant of it, comparing
// whole path segments — never a substring match — so a coincidental
// occurrence of dir's name elsewhere in p's path cannot false-positive.
func underDir(p, dir string) (rel string, ok bool) {
	if dir == "" {
		return "", false
	}
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	prefix := dir + string(filepath.Separator)
	if p == dir || !strings.HasPrefix(p, prefix) {
		return "", false
	}
	return filepath.ToSlash(p[len(prefix):]), true
}

// normalizeRef resolves one §4 reference against home, the referring plan's
// own corpus-relative directory ("docs/plans"): a bare filename (no "/") is
// home-relative, and a "./" or "../"-prefixed reference is resolved against
// home and cleaned. Both mirror scripts/secmeta.py's resolve_ref, which
// implements only the bare-filename arm — 026 review round 2 ruled that the
// port should not replicate that gap, filing it there as a follow-up
// instead so the two reconverge. An already corpus-relative reference
// ("docs/specs/...", "docs/plans/...") is returned unchanged.
func normalizeRef(ref, home string) string {
	if ref == "" {
		return ref
	}
	if !strings.Contains(ref, "/") {
		return home + "/" + ref
	}
	// §4: a repo-relative reference's leading "/" is optional —
	// "docs/specs/x.md" and "/docs/specs/x.md" are the same reference.
	ref = strings.TrimPrefix(ref, "/")
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return path.Clean(home + "/" + ref)
	}
	return ref
}

// normalizeList applies normalizeRef to every entry.
func normalizeList(refs []string, home string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = normalizeRef(r, home)
	}
	return out
}

// Section returns the 026 §2.1 outcome for one spec section, addressed by a
// §4 spec reference — a bare filename, a repo-relative path, or an absolute
// CorpusDoc.Path, from either form the corpus was loaded in — and its bare
// anchor, e.g. "docs/specs/026-design-doc-queries.md", "sec-2.1". It also
// returns every plan whose claim on the section is `full` or `partial` — at
// any status, since a caller needs to tell an accepted plan that may still
// need executing from a superseded one that is done, and from a draft one
// still awaiting acceptance (026 §2.4) — deduplicated and sorted ascending
// by Path.
func (ix *PlanIndex) Section(specPath, anchor string) (PlanningOutcome, []CoveringPlan) {
	key := sectionKey{spec: resolveDoc(specPath, ix.specCanon, ix.specDir), anchor: anchor}

	var hasFull, hasPartial, hasNone, hasClosedPartial bool
	var covering []CoveringPlan
	seen := map[string]bool{} // a plan claiming one section twice reports once

	for _, c := range ix.claims[key] {
		if (c.level == "full" || c.level == "partial") && !seen[c.plan] {
			seen[c.plan] = true
			covering = append(covering, CoveringPlan{Path: c.plan, Status: c.status, Level: c.level})
		}
		if !discharges(c.status) {
			continue // draft: not yet owed nor owing planning (026 §2.1)
		}
		switch c.level {
		case "full":
			hasFull = true
		case "partial":
			hasPartial = true
			if ix.closes(c.fullCoverageWith, key, c.plan) {
				hasClosedPartial = true
			}
		case "none":
			hasNone = true
		default:
			// Not full, partial, or none: scripts/secmeta.py's
			// check_coverage_entry already rejects a missing or unknown
			// coverage level, so a committed corpus cannot reach this.
			// The claim simply decides nothing about the outcome.
		}
	}
	sort.Slice(covering, func(i, j int) bool { return covering[i].Path < covering[j].Path })

	outcome := Unplanned
	switch {
	case hasFull || hasClosedPartial:
		outcome = Full
	case hasPartial:
		outcome = Partial
	case hasNone:
		outcome = BoundOnly
	}
	return outcome, covering
}

// closes reports whether a partial claim's fullCoverageWith discharges the
// section: non-empty, and every named plan is not the claiming plan itself,
// discharges (accepted or superseded), and itself contributes full or
// partial coverage to the same section (026 §2.1; scripts/secmeta.py's
// cross_check enforces the same three refusals plus the self-reference
// one). fullCoverageWith is checked, never trusted — an empty list, a draft
// target, the claiming plan itself, a target contributing none, or a target
// that does not cover this section at all all fail this check and leave the
// claim merely partial.
func (ix *PlanIndex) closes(with []string, key sectionKey, self string) bool {
	if len(with) == 0 {
		return false
	}
	for _, sibling := range with {
		if sibling == self {
			return false
		}
		if !discharges(ix.status[sibling]) {
			return false
		}
		if !ix.contributes(sibling, key) {
			return false
		}
	}
	return true
}

// contributes reports whether plan has a discharging claim of level full or
// partial against key.
func (ix *PlanIndex) contributes(plan string, key sectionKey) bool {
	for _, c := range ix.claims[key] {
		if c.plan == plan && discharges(c.status) && (c.level == "full" || c.level == "partial") {
			return true
		}
	}
	return false
}
