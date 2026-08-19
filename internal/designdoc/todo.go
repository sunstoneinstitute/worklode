package designdoc

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// TodoItem is one unit of work remaining before a spec is fully implemented
// (026 §2.4). Every item is typed by the act that discharges it, because
// that act decides who can perform it.
//
// Anchor and Anchors are disjoint, and which is filled says what shape the
// item is: a plan-level item is attributed to one section and fills Anchor;
// an unplanned or partial item collapses a document's whole planning gap and
// fills Anchors; the document-level acceptance item fills neither. Heading
// follows the same split — the section's heading on a plan-level item, the
// document's title on the other two.
type TodoItem struct {
	Type    string   // unplanned | partial | plan-draft | unexecuted | blocked
	Doc     string   // repo-relative spec path the item belongs to
	Anchor  string   // "sec-9.2"; empty on a document-level item
	Anchors []string // the sections a collapsed unplanned/partial item names, in document order
	Heading string   // section heading on a plan-level item, document title otherwise
	Plan    string   // repo-relative plan path; empty for unplanned and partial
	Task    string   // "WL-42"; empty when the plan names none
	Detail  string   // one line naming why
}

// The five item types of 026 §2.4's table.
const (
	TodoUnplanned  = "unplanned"  // no plan covers the section
	TodoPartial    = "partial"    // covered only partial, nothing closes it
	TodoPlanDraft  = "plan-draft" // a draft plan covers it: a human must accept
	TodoUnexecuted = "unexecuted" // covering plan accepted, task absent or open
	TodoBlocked    = "blocked"    // covering plan accepted, a required plan is not discharged
)

// Diagnostics is the footer: what the walk did not do, and why an answer may
// be narrower than the question.
type Diagnostics struct {
	Unfollowed []string // requires edges not walked (no Deps, or target outside the corpus)
	Cycles     []string // requires cycles met during the walk
	Notes      []string // degradations, e.g. no closure lookup
}

// TodoOptions configures one walk. Closed is the injected task-closure
// lookup: closure is the server's answer, never a state string (026 §2.4),
// so this package never derives it. A nil Closed is the offline case — the
// planning half still answers and every task state reads as unknown.
type TodoOptions struct {
	Deps   bool
	Closed func(taskID string) (closed bool, known bool)
}

// Todo walks specPath's current sections, the plans covering them, and those
// plans' execution tasks, and returns one ordered work list (026 §2.4).
//
// specPath is any §4 path reference — bare filename, repo-relative with or
// without a leading "/", or an absolute CorpusDoc.Path; a "#sec-N" fragment
// is ignored, since the walk is always whole-document. NO-SPEC is an error
// rather than an empty run: an empty list means the spec is finished, and
// nothing else may print one.
func Todo(docs []CorpusDoc, specPath string, opts TodoOptions) ([]TodoItem, Diagnostics, error) {
	w := newTodoWalk(docs, opts)
	start, err := w.resolve(specPath)
	if err != nil {
		return nil, Diagnostics{}, err
	}
	if opts.Closed == nil {
		w.diag.Notes = append(w.diag.Notes,
			"no task-closure lookup: every plan's execution state reads as unknown, "+
				"and no `blocked` item is emitted (026 §2.4)")
	}
	w.walk(start)
	return w.ordered(), w.diag, nil
}

// rankedItem is one item with its sort key: topological rank over plan
// requires first, then the order documents were walked in, then the spec's
// own section document order, then plan path (026 §2.4).
type rankedItem struct {
	item     TodoItem
	rank     int
	docOrder int
	position int // negative for a document-level item, so it leads its document
}

// Positions ahead of any section, ordering a document's document-level items
// among themselves: the acceptance decision leads, then the collapsed
// planning gaps — nothing blocks writing a plan — then the plan items in the
// document's own section order (026 §2.4).
const (
	posAcceptance = -3
	posUnplanned  = -2
	posPartial    = -1
)

type todoWalk struct {
	opts                 TodoOptions
	specDir, planDir     string
	specCanon, planCanon string
	byPath               map[string]CorpusDoc    // canonical repo-relative path -> doc
	frontmatter          map[string]*Frontmatter // same key; nil when unparseable
	replacedBy           map[sectionKey][]string // target section -> replacing documents
	ix                   *PlanIndex
	visited              map[string]bool
	docOrder             map[string]int
	decidedPlan          map[string]bool // a plan owes at most one item, whatever it covers
	rankCache            map[string]int
	items                []rankedItem
	diag                 Diagnostics
}

func newTodoWalk(docs []CorpusDoc, opts TodoOptions) *todoWalk {
	specDir, planDir := corpusDirs(docs)
	w := &todoWalk{
		opts:        opts,
		specDir:     specDir,
		planDir:     planDir,
		byPath:      make(map[string]CorpusDoc, len(docs)),
		frontmatter: make(map[string]*Frontmatter, len(docs)),
		replacedBy:  make(map[sectionKey][]string),
		ix:          NewPlanIndex(docs),
		visited:     make(map[string]bool),
		docOrder:    make(map[string]int),
		decidedPlan: make(map[string]bool),
		rankCache:   make(map[string]int),
	}
	w.specCanon, w.planCanon = w.ix.specCanon, w.ix.planCanon
	for _, d := range docs {
		canon := w.canon(d)
		w.byPath[canon] = d
		w.frontmatter[canon] = docFrontmatter(d)
	}
	for _, d := range docs {
		w.indexSupersession(d)
	}
	return w
}

// canon is d's path in the canonical corpus-relative form every lookup here
// is keyed by, whichever form the corpus was loaded in.
func (w *todoWalk) canon(d CorpusDoc) string {
	if d.Kind == "plan" {
		return resolveDoc(d.Path, w.planCanon, w.planDir)
	}
	return resolveDoc(d.Path, w.specCanon, w.specDir)
}

// indexSupersession records both directions of d's supersession edges
// against the section they land on. Reading `replaces` and `isReplacedBy`
// and unioning them means a half-maintained mirror still registers, matching
// scripts/currentspec.py.
func (w *todoWalk) indexSupersession(d CorpusDoc) {
	canon := w.canon(d)
	home := path.Dir(canon)
	for _, e := range d.Edges {
		if e.Target == "NO-SPEC" {
			continue
		}
		switch e.Rel {
		case "replaces":
			key := sectionKey{spec: normalizeRef(e.Target, home), anchor: e.TargetAnchor}
			w.replacedBy[key] = append(w.replacedBy[key], canon)
		case "isReplacedBy":
			key := sectionKey{spec: canon, anchor: e.SrcAnchor}
			w.replacedBy[key] = append(w.replacedBy[key], normalizeRef(e.Target, home))
		}
	}
}

// effective reports whether a claim made by the document at src already
// holds (026 §3.1): a draft's claim is a proposal and drops nothing, while a
// document outside the corpus cannot be status-checked and is trusted.
func (w *todoWalk) effective(src string) bool {
	d, ok := w.byPath[src]
	if !ok {
		return true
	}
	return d.Status == "accepted" || d.Status == "superseded"
}

// dropped reports whether an effective replaces names this section; anchor
// "" asks about the whole document.
func (w *todoWalk) dropped(docPath, anchor string) bool {
	for _, src := range w.replacedBy[sectionKey{spec: docPath, anchor: anchor}] {
		if w.effective(src) {
			return true
		}
	}
	return false
}

// resolve canonicalises the ref the caller named and checks it addresses a
// spec or ADR this corpus holds.
func (w *todoWalk) resolve(ref string) (string, error) {
	base, _ := SplitFragment(strings.TrimSpace(ref))
	if base == "" {
		return "", fmt.Errorf("no document reference given")
	}
	if base == "NO-SPEC" {
		return "", NoSpecError(ref)
	}
	canon := resolveDoc(base, w.specCanon, w.specDir)
	d, ok := w.byPath[canon]
	if !ok {
		return "", fmt.Errorf("%s: no such document in this corpus", ref)
	}
	if d.Kind == "plan" {
		return "", fmt.Errorf("%s is a plan; this walk starts from a spec or ADR", ref)
	}
	return canon, nil
}

// walk visits start and, under Deps, everything it transitively requires.
// The requires graph may contain cycles — 025 and 026 require each other —
// so a revisit is recorded and the walk continues rather than failing.
func (w *todoWalk) walk(start string) {
	queue := []string{start}
	w.visited[start] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		w.docOrder[cur] = len(w.docOrder)
		w.emitDoc(cur)
		for _, req := range w.requires(cur) {
			if !w.opts.Deps {
				w.diag.Unfollowed = appendUnique(w.diag.Unfollowed,
					fmt.Sprintf("%s requires %s", path.Base(cur), path.Base(req)))
				continue
			}
			if d, ok := w.byPath[req]; !ok || d.Kind == "plan" {
				w.diag.Unfollowed = appendUnique(w.diag.Unfollowed,
					fmt.Sprintf("%s requires %s, which is not a spec in this corpus",
						path.Base(cur), path.Base(req)))
				continue
			}
			if w.visited[req] {
				if cycle := w.cycleThrough(req, cur); cycle != "" {
					w.diag.Cycles = appendUnique(w.diag.Cycles, cycle)
				}
				continue
			}
			w.visited[req] = true
			queue = append(queue, req)
		}
	}
}

// requires returns docPath's outgoing requires targets, canonicalised. The
// relation is not in CorpusDoc.Edges — EdgeMeta is 025 §16.2's sync-projected
// set, which never carries it — so it is read from the frontmatter.
func (w *todoWalk) requires(docPath string) []string {
	fm := w.frontmatter[docPath]
	if fm == nil {
		return nil
	}
	home := path.Dir(docPath)
	out := make([]string, 0, len(fm.Requires))
	for _, ref := range fm.Requires {
		base, _ := SplitFragment(ref)
		if base == "" || base == "NO-SPEC" {
			continue
		}
		out = append(out, normalizeRef(base, home))
	}
	return out
}

// cycleThrough renders the requires cycle closed by the edge from -> to,
// or "" when the two are merely reachable by different routes. The node list
// is rotated to start at its lexicographically smallest member so the same
// loop renders identically whichever edge closed it.
func (w *todoWalk) cycleThrough(from, to string) string {
	nodes := w.requiresPath(from, to)
	if nodes == nil {
		return ""
	}
	smallest := 0
	for i, n := range nodes {
		if n < nodes[smallest] {
			smallest = i
		}
	}
	rotated := append(append([]string{}, nodes[smallest:]...), nodes[:smallest]...)
	names := make([]string, 0, len(rotated)+1)
	for _, n := range rotated {
		names = append(names, path.Base(n))
	}
	return strings.Join(append(names, names[0]), " -> ")
}

// requiresPath returns the shortest requires path from `from` to `to`
// inclusive, or nil when there is none. Neighbours are visited in
// frontmatter order, so the answer does not move run to run.
func (w *todoWalk) requiresPath(from, to string) []string {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			var out []string
			for n := cur; n != ""; n = prev[n] {
				out = append([]string{n}, out...)
			}
			return out
		}
		for _, next := range w.requires(cur) {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			queue = append(queue, next)
		}
	}
	return nil
}

// emitDoc contributes one document's items: the acceptance decision a draft
// owes, and one pass over its current sections.
func (w *todoWalk) emitDoc(docPath string) {
	d := w.byPath[docPath]
	if d.Status == "draft" {
		// 026 §2.4: the acceptance item leads the list, it does not replace
		// it. §2.1's "a draft spec is not yet owed planning" is the right
		// rule for the corpus-wide --needs-planning sweep, and the wrong one
		// here — naming one document is itself the statement that this spec
		// is worth asking about, so the caller gets its sections whatever
		// the header says. posAcceptance ranks it ahead of them.
		w.add(rankedItem{
			item: TodoItem{
				Type: TodoPlanDraft, Doc: docPath, Heading: d.Title,
				Detail: "document is draft: acceptance is the first act (026 §2.4)",
			},
			docOrder: w.docOrder[docPath], position: posAcceptance,
		})
	}
	if d.Status == "superseded" || w.dropped(docPath, "") {
		w.diag.Notes = append(w.diag.Notes,
			fmt.Sprintf("%s is superseded: none of its sections still state the design",
				path.Base(docPath)))
		return
	}
	var unplanned, partial []string
	for _, sec := range d.Sections {
		if w.dropped(docPath, sec.Anchor) {
			continue
		}
		switch w.emitSection(docPath, sec) {
		case TodoUnplanned:
			unplanned = append(unplanned, sec.Anchor)
		case TodoPartial:
			partial = append(partial, sec.Anchor)
		}
	}
	w.emitGap(docPath, d.Title, TodoUnplanned, unplanned, posUnplanned)
	w.emitGap(docPath, d.Title, TodoPartial, partial, posPartial)
}

// emitGap records a document's whole planning gap of one type as a single
// item naming its anchors. Writing a plan is one act and one plan covers many
// sections, exactly as executing a plan is one act however many sections it
// covers (026 §2.4) — emitting one item per section buried the executable
// tail of this corpus's own answers under fifty-odd section names.
func (w *todoWalk) emitGap(docPath, title, typ string, anchors []string, position int) {
	if len(anchors) == 0 {
		return
	}
	w.add(rankedItem{
		item: TodoItem{
			Type: typ, Doc: docPath, Anchors: anchors, Heading: title,
			Detail: gapDetail(typ, len(anchors)),
		},
		docOrder: w.docOrder[docPath], position: position,
	})
}

// gapDetail is a collapsed item's one-line reason. The CLI prints it
// verbatim, so it has to read correctly at one section and at fifty — hence the verb
// and the pronoun varying with the count, rather than a bare count pasted
// into one fixed sentence.
func gapDetail(typ string, n int) string {
	switch {
	case typ == TodoUnplanned && n == 1:
		return "1 section has no covering plan"
	case typ == TodoUnplanned:
		return fmt.Sprintf("%d sections have no covering plan", n)
	case n == 1:
		return "1 section is only partly covered, and no plan completes it"
	default:
		return fmt.Sprintf("%d sections are only partly covered, and no plan completes them", n)
	}
}

// emitSection emits one section's plan-level items and reports the
// section-level gap type it still has ("" when none), which emitDoc collapses
// into one item per document.
func (w *todoWalk) emitSection(docPath string, sec SectionMeta) string {
	outcome, covering := w.ix.Section(docPath, sec.Anchor)

	// The section-level gap, discharged by writing a plan. It is suppressed
	// when a draft plan already covers the section: 026 §2.4 calls reporting
	// that as unplanned "the opposite error", since the plan exists and
	// rewriting it wastes the drafting — the pending act is the acceptance
	// the plan-draft item below carries. §2.4 extends the same suppression to
	// `partial` when the draft plan claims `full`. A section only a draft
	// plan binds at `none` is a different case: §2.4 forbids a plan-draft
	// item there, not an item at all, and nothing covers it, so it still
	// needs planning.
	gap := ""
	switch {
	case outcome == Unplanned && len(covering) == 0:
		gap = TodoUnplanned
	case outcome == Partial && !coveredFullByDraft(covering):
		gap = TodoPartial
	}

	// Every accepted covering plan is descended into, whatever the section's
	// outcome: 026 §2.4's unexecuted and blocked rows key on the plan's
	// status, not on the outcome, so gating this on `full` would hide an
	// accepted partial plan that has never been executed.
	for _, plan := range covering {
		if plan.Status == "draft" || plan.Status == "accepted" {
			// A cycle is a corpus fact — independent of the plan's status,
			// of whether the plan owes an item, and of whether a closure
			// lookup is available — so it is noted before any of those are
			// decided.
			w.notePlanCycles(plan.Path)
		}
		switch plan.Status {
		case "draft":
			w.emitPlan(docPath, sec, plan, TodoPlanDraft,
				"plan is draft: accepting it is a human act (025 §7)")
		case "accepted":
			w.emitAcceptedPlan(docPath, sec, plan)
		}
		// A superseded plan is spent: the work it covered is done, and there
		// is no task state left to consult (026 §2.1, §2.4).
	}
	return gap
}

// coveredFullByDraft reports whether some draft plan claims full coverage,
// which makes "covered only partial" false — the pending act there is
// acceptance, not more planning.
func coveredFullByDraft(covering []CoveringPlan) bool {
	for _, p := range covering {
		if p.Status == "draft" && p.Level == "full" {
			return true
		}
	}
	return false
}

// emitAcceptedPlan decides what an accepted covering plan still owes:
// nothing when its task is closed, `blocked` when a plan it requires is not
// itself discharged, `unexecuted` otherwise.
func (w *todoWalk) emitAcceptedPlan(docPath string, sec SectionMeta, plan CoveringPlan) {
	if w.decidedPlan[plan.Path] {
		return
	}
	task := w.taskOf(plan.Path)
	closed, known := w.closed(task)
	if closed {
		w.decidedPlan[plan.Path] = true
		return
	}
	// Offline, `blocked` is not emitted at all (026 §2.4): it is a statement
	// about another plan's task state, which is precisely what is
	// unavailable. Letting it through would make an item's type depend on
	// the caller's connectivity, so the same corpus would describe the same
	// work differently from a laptop on a train.
	if w.opts.Closed != nil {
		if blockers := w.blockers(plan.Path); len(blockers) > 0 {
			w.emitPlan(docPath, sec, plan, TodoBlocked,
				"requires "+strings.Join(blockers, ", ")+", not discharged")
			return
		}
	}
	switch {
	case task == "":
		w.emitPlan(docPath, sec, plan, TodoUnexecuted, "plan names no execution task")
	case !known:
		w.emitPlan(docPath, sec, plan, TodoUnexecuted, "task "+task+": state unknown")
	default:
		w.emitPlan(docPath, sec, plan, TodoUnexecuted, "task "+task+" is open")
	}
}

// emitPlan records one plan-level item, attributed to the first section of
// this spec the plan covers. Every item in 026 §2.4 is typed by the act that
// discharges it, and accepting or executing a plan is one act — so a plan
// covering six sections owes one item, not six identical rows.
func (w *todoWalk) emitPlan(docPath string, sec SectionMeta, plan CoveringPlan, typ, detail string) {
	if w.decidedPlan[plan.Path] {
		return
	}
	w.decidedPlan[plan.Path] = true
	w.add(rankedItem{
		item: TodoItem{
			Type: typ, Doc: docPath, Anchor: sec.Anchor, Heading: sec.Heading,
			Plan: plan.Path, Task: w.taskOf(plan.Path), Detail: detail,
		},
		rank:     w.planRank(plan.Path),
		docOrder: w.docOrder[docPath],
		position: sec.Position,
	})
}

func (w *todoWalk) add(it rankedItem) {
	w.items = append(w.items, it)
}

// taskOf is the plan's transitional `task` key (026 §5.2), naming the task
// its execution hangs off in today's tracker.
func (w *todoWalk) taskOf(planPath string) string {
	if fm := w.frontmatter[planPath]; fm != nil {
		return fm.Task
	}
	return ""
}

// closed asks the injected lookup. No lookup, or no task to ask about,
// leaves closure unknown — which is never evidence of closure.
func (w *todoWalk) closed(task string) (closed, known bool) {
	if task == "" || w.opts.Closed == nil {
		return false, false
	}
	return w.opts.Closed(task)
}

// blockers lists the plans planPath requires that are not themselves
// discharged, by canonical repo-relative path. Non-plan requires targets are
// another spec's business, not this plan's blocker.
func (w *todoWalk) blockers(planPath string) []string {
	var out []string
	for _, req := range w.requires(planPath) {
		d, ok := w.byPath[req]
		if !ok || d.Kind != "plan" || w.planDischarged(req) {
			continue
		}
		out = append(out, req)
	}
	return out
}

// notePlanCycles records any requires cycle planPath sits on. A plan cycle
// makes each member block the other forever, and silent mutual blocking is
// indistinguishable from real ordering — the same reasoning that puts a
// spec-level cycle in the footer (026 §2.4).
func (w *todoWalk) notePlanCycles(planPath string) {
	for _, req := range w.requires(planPath) {
		if d, ok := w.byPath[req]; !ok || d.Kind != "plan" {
			continue
		}
		if cycle := w.cycleThrough(req, planPath); cycle != "" {
			w.diag.Cycles = appendUnique(w.diag.Cycles, cycle)
		}
	}
}

// planDischarged reports whether a required plan is done: superseded (spent),
// or accepted with a closed execution task. A plan outside the corpus cannot
// be judged and is not treated as a blocker.
func (w *todoWalk) planDischarged(planPath string) bool {
	d, ok := w.byPath[planPath]
	if !ok {
		return true
	}
	switch d.Status {
	case "superseded":
		return true
	case "accepted":
		closed, known := w.closed(w.taskOf(planPath))
		return closed && known
	}
	return false
}

// planRank is the plan's depth in the requires graph over plans: 0 for one
// that requires nothing, otherwise one more than the deepest plan it
// requires. The on-stack guard exists because requires cycles are legal —
// 026 §2.4 says the graph may contain them and reports one rather than
// failing, unlike §4.1's gate, which refuses cycles in the section-level
// amends/replaces graph. A reader who meets one is better served by an
// order than by a hang.
func (w *todoWalk) planRank(planPath string) int {
	return w.rankOf(planPath, map[string]bool{})
}

// rankOf memoizes the depth. Ranks within a cycle depend on which member the
// recursion entered from, so they are arbitrary — but the entry point is
// fixed by the walk, so they are the same arbitrary ranks every run, which is
// what the ordering guarantee needs.
func (w *todoWalk) rankOf(planPath string, onStack map[string]bool) int {
	if r, ok := w.rankCache[planPath]; ok {
		return r
	}
	if onStack[planPath] {
		return 0
	}
	onStack[planPath] = true
	defer delete(onStack, planPath)

	rank := 0
	for _, req := range w.requires(planPath) {
		if d, ok := w.byPath[req]; !ok || d.Kind != "plan" {
			continue
		}
		if r := w.rankOf(req, onStack) + 1; r > rank {
			rank = r
		}
	}
	w.rankCache[planPath] = rank
	return rank
}

// ordered sorts the collected items into the execution queue 026 §2.4
// specifies: topologically over plan requires, ties broken by document walk
// order, then the spec's own section order, then plan path.
func (w *todoWalk) ordered() []TodoItem {
	sort.SliceStable(w.items, func(i, j int) bool {
		a, b := w.items[i], w.items[j]
		switch {
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.docOrder != b.docOrder:
			return a.docOrder < b.docOrder
		case a.position != b.position:
			return a.position < b.position
		case a.item.Plan != b.item.Plan:
			return a.item.Plan < b.item.Plan
		default:
			return a.item.Type < b.item.Type
		}
	})
	if len(w.items) == 0 {
		return nil
	}
	out := make([]TodoItem, len(w.items))
	for i, it := range w.items {
		out[i] = it.item
	}
	return out
}

// docFrontmatter recovers a document's parsed frontmatter from the source
// LoadSyncCorpus captured. As with planCoverageEntries, d.Source is
// required: a hand-built CorpusDoc without it reads as carrying no keys.
func docFrontmatter(d CorpusDoc) *Frontmatter {
	doc, err := Parse(d.Source)
	if err != nil {
		return nil
	}
	return doc.Frontmatter
}

// corpusDirs reports the spec-corpus and plan-corpus directories exactly as
// docs were loaded — absolute when the caller reached LoadSyncCorpus through
// FindCorpus, repo-relative otherwise. "" when no document of that kind was
// loaded.
func corpusDirs(docs []CorpusDoc) (specDir, planDir string) {
	for _, d := range docs {
		switch d.Kind {
		case "plan":
			if planDir == "" {
				planDir = filepath.Dir(d.Path)
			}
		case "spec", "adr":
			if specDir == "" {
				specDir = filepath.Dir(d.Path)
			}
		}
	}
	return specDir, planDir
}

// appendUnique appends s unless xs already holds it.
func appendUnique(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}
