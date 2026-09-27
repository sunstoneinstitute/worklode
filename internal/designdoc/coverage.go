package designdoc

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// PlanningOutcome is where one spec section sits (WL-SPEC-78 §1.3).
type PlanningOutcome string

const (
	Planned   PlanningOutcome = "planned"
	PlanDraft PlanningOutcome = "plan-draft"
	Deferred  PlanningOutcome = "deferred"
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

// CoveringPlan is one plan covering a section.
type CoveringPlan struct {
	Path   string // repo-relative
	Status string // "accepted" | "superseded" | "draft"
}

// claim is one plan's covers edge onto one spec section, resolved once at
// index build time so Section need not re-parse frontmatter per query.
type claim struct {
	plan   string // repo-relative
	status string // the plan's frontmatter status
}

// deferral is one plan's explicit handoff of a section to a named owner
// (026 §5.3), resolved once at index build time the same way claim is:
// Section need not re-parse frontmatter per query.
type deferral struct {
	plan   string // repo-relative
	status string // the plan's frontmatter status
	owner  string // repo-relative reference to the document the section is handed to
}

// discharges reports whether status is in WL-SPEC-78 §1.3's discharging set:
// accepted or superseded. A superseded plan is spent (accepted, then
// executed) and discharges what it covered exactly as an accepted plan does.
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

	// unresolved names every `covers` target that resolved to a path no
	// document in the corpus holds, "<plan> covers <raw target>" (WL-756).
	// Such a claim is filed under a key no section ever queries, so the
	// sections it names read as unplanned; a caller surfaces this as a
	// stated gap rather than letting it pass as data.
	unresolved []string
}

// UnresolvedTargets is every `covers` target that named no document in the
// corpus, deduplicated and sorted.
func (ix *PlanIndex) UnresolvedTargets() []string { return ix.unresolved }

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
		projectKey: projectKey,
	}
	ix.specDir, ix.planDir = corpusDirs(docs)
	ix.specCanon, ix.planCanon = canonDirs(ix.specDir, ix.planDir)
	ix.buildResolver(docs)

	// knownSpecs is every spec/ADR document's own live corpus identity —
	// built once so a covers/defers reference written against an old
	// numbered filename (WL-404) can be recognised as the same document a
	// plan-free slug now names, without ever guessing at a document the
	// corpus does not actually hold.
	knownSpecs := make(map[string]bool, len(docs))
	for _, d := range docs {
		if d.Kind == "plan" {
			continue
		}
		knownSpecs[resolveDoc(d.Path, ix.specCanon, ix.specDir)] = true
	}

	unresolved := map[string]bool{}
	for _, d := range docs {
		if d.Kind != "plan" {
			continue
		}
		plan := resolveDoc(d.Path, ix.planCanon, ix.planDir)
		home := path.Dir(plan)
		for _, e := range d.Edges {
			if e.Rel != "covers" && e.Rel != "defers" {
				continue
			}
			if e.TargetAnchor == "" || e.Target == "NO-SPEC" {
				// A whole-document covers names no section a coverage query
				// can use, and NO-SPEC has no sections to cover (026 §2.1,
				// §4.3). A defers entry with no fragment is rejected at write
				// time (026 §5.3); this skip mirrors covers defensively.
				continue
			}
			target := resolveNumberedAlias(ix.normalizeRef(e.Target, home), knownSpecs)
			key := sectionKey{spec: target, anchor: e.TargetAnchor}
			if e.Rel == "defers" {
				owner := ix.normalizeRef(e.Owner, home)
				ix.defers[key] = append(ix.defers[key], deferral{plan: plan, status: d.Status, owner: owner})
				continue
			}
			if !knownSpecs[target] {
				unresolved[plan+" covers "+e.Target] = true
			}
			ix.claims[key] = append(ix.claims[key], claim{plan: plan, status: d.Status})
		}
	}
	for s := range unresolved {
		ix.unresolved = append(ix.unresolved, s)
	}
	sort.Strings(ix.unresolved)
	return ix
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
// same-corpus reference, `requires` naming another document
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

// numberPrefixPattern is the leading "<digits>-" a numbered corpus filename
// carries (WL-404) — "045-per-project-workflows.md" — even once a spec's
// backbone slug has dropped the number 025 §17 minted its ordinal from.
var numberPrefixPattern = regexp.MustCompile(`^\d+-`)

// resolveNumberedAlias recognises ref as a numbered-filename alias of a spec
// or ADR the corpus already holds under the number-stripped slug that is its
// live identity: docs/authoring-design-docs.md's own examples, and the only
// form scripts/secmeta.py's on-disk check accepts, still write a numbered
// spec by its git filename, while the document's `covers:`-matching identity
// is the slug the backbone assigned it (WL-404).
//
// ref is returned unchanged when it is already a known identity (the common
// case — most specs' slugs still carry their number), or when stripping its
// basename's number prefix names no document the corpus actually holds:
// resolving that case anyway would risk matching two unrelated documents
// that share nothing but leading digits, exactly what resolveDocRef's own
// number-form guard (internal/store/docedges.go) refuses for the same
// reason at write time.
func resolveNumberedAlias(ref string, known map[string]bool) string {
	if known[ref] {
		return ref
	}
	dir, base := path.Split(ref)
	loc := numberPrefixPattern.FindStringIndex(base)
	if loc == nil {
		return ref
	}
	if alias := dir + base[loc[1]:]; known[alias] {
		return alias
	}
	return ref
}

// Section returns the WL-SPEC-78 §1.3 outcome for one spec section,
// addressed by a §4 spec reference — a bare filename, a repo-relative path,
// or an absolute CorpusDoc.Path, from either form the corpus was loaded in —
// and its bare anchor, e.g. "docs/specs/026-design-doc-queries.md", "sec-2.1".
// It also returns every plan covering the section, at any status, since a
// caller needs to tell an accepted plan that may still need executing from a
// superseded one that is done and from a draft one awaiting acceptance —
// deduplicated and sorted ascending by Path.
//
// A covers edge means the plan builds the whole rule, so one accepted or
// superseded plan plans the section, but only once no draft plan also
// covers it: until then it is PlanDraft. With no covering plan, a deferral
// makes it Deferred.
//
// The third return is the deferred-to owner: non-empty only when the outcome
// is Deferred, in which case it is every distinct owner an accepted-or-
// superseded plan's `defers` names for this section, sorted and comma-joined
// — the same join spelling the store's NeedsPlanning uses.
func (ix *PlanIndex) Section(specPath, anchor string) (PlanningOutcome, []CoveringPlan, string) {
	key := sectionKey{spec: resolveDoc(specPath, ix.specCanon, ix.specDir), anchor: anchor}

	var discharged, draft bool
	var covering []CoveringPlan
	seen := map[string]bool{} // a plan claiming one section twice reports once
	for _, c := range ix.claims[key] {
		if !seen[c.plan] {
			seen[c.plan] = true
			covering = append(covering, CoveringPlan{Path: c.plan, Status: c.status})
		}
		discharged = discharged || discharges(c.status)
		draft = draft || c.status == "draft"
	}
	sort.Slice(covering, func(i, j int) bool { return covering[i].Path < covering[j].Path })

	switch {
	case draft:
		return PlanDraft, covering, ""
	case discharged:
		return Planned, covering, ""
	}
	if owner := ix.deferredOwner(key); owner != "" {
		return Deferred, covering, owner
	}
	return Unplanned, covering, ""
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
