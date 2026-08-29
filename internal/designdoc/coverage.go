package designdoc

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// PlanningOutcome is where one spec section sits, per 026 §2.1, relative to
// the corpus's discharging-plan coverage.
type PlanningOutcome string

const (
	Full      PlanningOutcome = "full"
	Partial   PlanningOutcome = "partial"
	Deferred  PlanningOutcome = "deferred"
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

// deferral is one plan's explicit handoff of a section to a named owner
// (026 §5.3), resolved once at index build time the same way claim is:
// Section need not re-parse frontmatter per query.
type deferral struct {
	plan   string // repo-relative
	status string // the plan's frontmatter status
	owner  string // repo-relative reference to the document the section is handed to
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
	defers map[sectionKey][]deferral
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

	// projectKey and the three fields below back normalizeRef's fallback
	// resolution (WL-409): a covers/defers target is always in a *different*
	// corpus from its referring plan, so when a bare reference does not name a
	// document at its home-relative guess, it is retried as a number, slug, or
	// <KEY>-<TYPE>-<n> shorthand against every document in the corpus — the
	// same forms ResolveRef resolves for `lode show`. projectKey is "" when
	// the caller has none (offline callers, and every existing test): the
	// fallback then declines every shorthand rather than guessing a project.
	projectKey    string
	resolveDocs   []model.Doc     // synthetic candidates for ResolveRef, ID = index into resolvePaths
	resolvePaths  []string        // resolveDocs[i]'s corpus-relative Path
	resolveByPath map[string]bool // every document's corpus-relative Path, for the "guess already matches" check
}

// NewPlanIndex indexes docs for Section queries: every plan document's
// coverage claims, plus (from any spec/ADR docs present) the spec-corpus
// directory a bare or absolute specPath resolves against. Non-plan documents
// otherwise contribute nothing to the claims themselves — this task's
// predicate only walks plan-side `covers` claims — but every document, of
// every kind, feeds the number/slug/shorthand resolver normalizeRef falls
// back to (WL-409).
//
// projectKey is the current repo's project key ("WL"), or "" when the caller
// has none (every offline caller, and every existing caller before WL-409):
// a covers/defers entry written as a <KEY>-<TYPE>-<n> shorthand then never
// resolves, exactly as before this existed, rather than resolving against a
// project the caller cannot actually vouch for.
func NewPlanIndex(docs []CorpusDoc, projectKey string) *PlanIndex {
	ix := &PlanIndex{
		claims:     make(map[sectionKey][]claim),
		defers:     make(map[sectionKey][]deferral),
		status:     make(map[string]string),
		projectKey: projectKey,
	}
	ix.specDir, ix.planDir = corpusDirs(docs)
	ix.specCanon, ix.planCanon = canonDirs(ix.specDir, ix.planDir)
	ix.buildResolver(docs)
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
			key := sectionKey{spec: ix.normalizeRef(rawTarget, home), anchor: anchor}
			ix.claims[key] = append(ix.claims[key], claim{
				plan:             plan,
				status:           d.Status,
				level:            entry.Coverage,
				fullCoverageWith: ix.normalizeList(entry.FullCoverageWith, home),
			})
		}
		for _, entry := range planDeferralEntries(d) {
			rawTarget, anchor := SplitFragment(entry.Spec)
			if anchor == "" || rawTarget == "NO-SPEC" {
				// Unlike covers, a defers entry with no #sec-N fragment is
				// rejected outright at write time (026 §5.3) — this mirrors
				// covers' own defensive skip rather than assuming the corpus
				// is already valid.
				continue
			}
			key := sectionKey{spec: ix.normalizeRef(rawTarget, home), anchor: anchor}
			ix.defers[key] = append(ix.defers[key], deferral{
				plan:   plan,
				status: d.Status,
				owner:  ix.normalizeRef(entry.To, home),
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

// planDeferralEntries recovers d's `defers` entries (026 §5.3), the same way
// planCoverageEntries recovers `covers`: CorpusDoc.Edges is 025 §16.2's
// sync-projected relation shape and does not carry the named owner, so this
// re-parses the frontmatter captured in d.Source. d.Source is required for
// the same reason planCoverageEntries states.
func planDeferralEntries(d CorpusDoc) DeferralList {
	doc, err := Parse(d.Source)
	if err != nil || doc.Frontmatter == nil {
		return nil
	}
	return doc.Frontmatter.Defers
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

// normalizeRef resolves one §4 reference against home, the referring
// document's own corpus-relative directory ("docs/plans"): a "./" or
// "../"-prefixed reference is resolved against home and cleaned, and an
// already corpus-relative reference ("docs/specs/...", "docs/plans/...") is
// returned unchanged. Both mirror scripts/secmeta.py's resolve_ref — 026
// review round 2 ruled the port should not replicate its bare-filename-only
// gap, filing it there as a follow-up instead so the two reconverge.
//
// A bare reference (no "/") is tried home-relative first — correct for a
// same-corpus reference, `requires` and `amends` naming another document
// right beside the referring one — and only when that guess names no
// document actually in the corpus is it retried as a number, slug, or
// <KEY>-<TYPE>-<n> shorthand against every document (WL-409): a covers or
// defers target is always a spec, which never lives in a plan's own
// directory, so the home-relative guess can never be right for it. On any
// resolveShorthand failure the guess stands unchanged, exactly as before
// this fallback existed — a typo reads as an unplanned section, not an error
// (026 review round 2).
func (ix *PlanIndex) normalizeRef(ref, home string) string {
	if ref == "" {
		return ref
	}
	if !strings.Contains(ref, "/") {
		guess := home + "/" + ref
		if !ix.resolveByPath[guess] {
			if resolved, ok := ix.resolveShorthand(ref); ok {
				return resolved
			}
		}
		return guess
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
func (ix *PlanIndex) normalizeList(refs []string, home string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = ix.normalizeRef(r, home)
	}
	return out
}

// buildResolver indexes every document in the corpus — of every kind, not
// only plans — by its own number, slug and kind, so normalizeRef's fallback
// can call ResolveRef against them the same way `lode show` resolves a
// number, slug or shorthand reference. resolveDocs[i]'s ID is i itself, so a
// resolved model.Doc maps straight back to resolvePaths[i] with no separate
// lookup.
func (ix *PlanIndex) buildResolver(docs []CorpusDoc) {
	ix.resolveDocs = make([]model.Doc, 0, len(docs))
	ix.resolvePaths = make([]string, 0, len(docs))
	ix.resolveByPath = make(map[string]bool, len(docs))
	for _, d := range docs {
		canon, dir := ix.specCanon, ix.specDir
		if d.Kind == "plan" {
			canon, dir = ix.planCanon, ix.planDir
		}
		p := resolveDoc(d.Path, canon, dir)
		ix.resolveByPath[p] = true
		ix.resolveDocs = append(ix.resolveDocs, model.Doc{
			ID: int64(len(ix.resolvePaths)), Kind: d.Kind, Number: d.Number,
			Slug: strings.TrimSuffix(path.Base(d.Path), ".md"),
		})
		ix.resolvePaths = append(ix.resolvePaths, p)
	}
}

// resolveShorthand resolves ref — a bare reference that named no document in
// the corpus at its home-relative guess — as a document number, slug or
// <KEY>-<TYPE>-<n> shorthand, against every document buildResolver indexed.
// false on any failure (no project key, not found, ambiguous, wrong kind,
// NO-SPEC): normalizeRef's guess stands unchanged in every such case, so a
// genuine miss degrades exactly as it did before this fallback existed.
//
// ResolveRef's bare-number and bare-slug forms match across the whole corpus
// with no project scoping — safe here because a covers/defers target is
// conventionally either a path or the shorthand (never a bare slug: 026
// review round 2's authoring guidance reserves that form for a same-corpus
// reference, which already resolves via the home-relative guess above and
// never reaches here) — so in practice only the shorthand form, which is
// key-scoped, is ever exercised through this path.
func (ix *PlanIndex) resolveShorthand(ref string) (string, bool) {
	if ix.projectKey == "" {
		return "", false
	}
	doc, _, err := ResolveRef(ix.resolveDocs, ix.projectKey, ref)
	if err != nil {
		return "", false
	}
	return ix.resolvePaths[doc.ID], true
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
//
// The third return is the deferred-to owner: non-empty only when the outcome
// is Deferred, in which case it is every distinct owner an accepted-or-
// superseded plan's `defers` names for this section (026 §5.3), sorted and
// comma-joined — the same join spelling internal/store/docs.go's
// NeedsPlanning uses, so the two consumers agree on both the outcome and its
// detail for the same section.
func (ix *PlanIndex) Section(specPath, anchor string) (PlanningOutcome, []CoveringPlan, string) {
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

	owner := ix.deferredOwner(key)

	// 026 §2.1's precedence over the undischarged readings: partial, then
	// deferred, then bound-only, then unplanned. Full (and a closed partial)
	// discharges the section outright and is decided first, exactly as
	// before defers was indexed.
	outcome := Unplanned
	switch {
	case hasFull || hasClosedPartial:
		outcome = Full
	case hasPartial:
		outcome = Partial
	case owner != "":
		outcome = Deferred
	case hasNone:
		outcome = BoundOnly
	}
	if outcome != Deferred {
		owner = ""
	}
	return outcome, covering, owner
}

// deferredOwner returns the comma-joined, sorted, deduplicated set of owners
// an accepted-or-superseded plan's `defers` names for key (026 §5.3) — the
// same "not draft" eligibility rule Section applies to a covers claim
// (discharges), not a separate rule invented for defers. "" when no such
// plan defers this section.
func (ix *PlanIndex) deferredOwner(key sectionKey) string {
	seen := map[string]bool{}
	var owners []string
	for _, d := range ix.defers[key] {
		if !discharges(d.status) || seen[d.owner] {
			continue
		}
		seen[d.owner] = true
		owners = append(owners, d.owner)
	}
	sort.Strings(owners)
	return strings.Join(owners, ",")
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
