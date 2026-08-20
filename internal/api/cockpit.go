// cockpit.go implements the project cockpit: GET /api/v1/projects/{id}/cockpit
// and the assembler shared with the project web page (see projectPage in
// web.go). The cockpit is a projection, never a stored workflow field —
// selectMode is a pure function of declared facts, and assembleProjectCockpit
// builds its output fresh from existing store readers on every call (spec
// 032). Work items, owner/delegate, evidence, blockers, repositories, and
// cost are all mapped directly from store.ListProjectWorkFacts (Task 3),
// (*store.Store).GetActor, ListRepos, and ProjectCost — no board adapter, no
// invented state. Pinned focus and the next governed decision are the curated
// v0 cards backed by the project row (migration 0013); each stays nil until a
// lead sets it via PinProjectFocus / SetProjectNextDecision.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// --- lifecycle mode ------------------------------------------------------

type cockpitMode string

const (
	modeEditorialDecision cockpitMode = "editorial_decision"
	modeApprovedLaunch    cockpitMode = "approved_launch"
	modeOperations        cockpitMode = "operations"
)

// modeFacts are the declared lifecycle facts that govern which cockpit mode
// a project is in. A project can never be in two modes at once because
// selectMode is a total, pure function of these facts, not a stored,
// independently-editable field.
type modeFacts struct {
	IntakeCandidate    bool
	PromotedFromIntake bool
	EnteredResearch    bool
}

// selectMode derives the cockpit's lifecycle mode entirely from declared
// facts: an intake candidate is always in editorial decision; a project
// promoted from intake but not yet in research is an approved launch
// awaiting Enter Research; everything else — including every project that
// predates spec 029/032 — is ordinary operations. ?variant= query parameters
// never reach this function and must never change its result.
func selectMode(f modeFacts) cockpitMode {
	if f.IntakeCandidate {
		return modeEditorialDecision
	}
	if f.PromotedFromIntake && !f.EnteredResearch {
		return modeApprovedLaunch
	}
	return modeOperations
}

// modeFactsForProject remains all-false until Part 2 stores spec-029
// promotion and Enter Research decisions. A current project is therefore an
// ordinary Operations project; query parameters are intentionally absent.
func modeFactsForProject(store.Project) modeFacts { return modeFacts{} }

// --- evidence classification ------------------------------------------------

// evidenceCategory is one of the four evidence categories spec 032 defines
// for every disclosed fact. Part 1 emits declared, user_reported, and
// observed only — it has no AI-produced recommendation, so evidenceRecommended
// is never assigned by stateEvidence, only reserved for a later part.
type evidenceCategory string

const (
	evidenceDeclared     evidenceCategory = "declared"
	evidenceUserReported evidenceCategory = "user_reported"
	evidenceObserved     evidenceCategory = "observed"
	evidenceRecommended  evidenceCategory = "recommended"
)

// evidenceLabels are the fixed display strings for each evidenceCategory,
// pinned by TestEvidenceCategoryLabel. Never derive a label by replacing
// underscores with spaces — evidenceUserReported hyphenates instead of
// spacing ("User-reported", not "User reported").
var evidenceLabels = map[evidenceCategory]string{
	evidenceDeclared:     "Declared",
	evidenceUserReported: "User-reported",
	evidenceObserved:     "Observed",
	evidenceRecommended:  "Recommended",
}

// Label returns c's fixed display text.
func (c evidenceCategory) Label() string {
	if l, ok := evidenceLabels[c]; ok {
		return l
	}
	return string(c)
}

// stateEvidence classifies the evidence behind a task's current state (or
// any other fact backed by an optional event): no event at all is a
// declared fact (the tracker's own state, asserted rather than witnessed);
// an event from an external system of record (github, flux, watcher, or the
// server's own system source) is observed; a cli-sourced lease event is
// observed too (the lease machinery enforces it, not a human's say-so),
// while any other cli-sourced event is user-reported (someone typed a
// command); anything else defaults to declared.
func stateEvidence(source, eventType string, hasEvent bool) evidenceCategory {
	if !hasEvent {
		return evidenceDeclared
	}
	switch source {
	case "github", "flux", "watcher", "system":
		return evidenceObserved
	case "cli":
		if strings.HasPrefix(eventType, "lease.") {
			return evidenceObserved
		}
		return evidenceUserReported
	default:
		return evidenceDeclared
	}
}

// operationsModeBasis is the fixed evidence basis for modeOperations, the
// only mode selectMode can produce until Part 2 stores promotion/Enter
// Research facts (see modeFactsForProject).
var operationsModeBasis = model.EvidenceSummary{
	Category: "declared",
	Summary:  "Existing Worklode project; no intake lifecycle facts are present",
}

// costWindow is how far back Cost looks from "now": 30 days, inclusive of
// today. Fixed rather than caller-controlled — the cockpit is a snapshot,
// not a report generator (that stays behind the existing bounded
// GET /api/v1/projects/{id}?from=&to= query parameters).
const costWindow = 30 * 24 * time.Hour

// --- assembly --------------------------------------------------------------

// assembleProjectCockpit builds the cockpit projection for one project,
// fresh on every call from GetProject, ListProjectWorkFacts, ListRepos, and
// ProjectCost. Returns store.ErrNotFound (via GetProject) when the project
// does not exist.
func (s *server) assembleProjectCockpit(ctx context.Context, id string) (*model.CockpitProjection, error) {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		return nil, err
	}
	facts, err := s.st.ListProjectWorkFacts(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := s.st.ListRepos(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.st.Now()
	cost, err := s.st.ProjectCost(ctx, id, now.Add(-costWindow), now)
	if err != nil {
		return nil, err
	}

	// actors caches actor lookups for the lifetime of this one projection —
	// a project's work commonly repeats the same owner/delegate across many
	// tasks, and this keeps each distinct actor to one GetActor round trip.
	actors := map[string]*store.Actor{}
	resolveActor := func(actorID string) (*store.Actor, error) {
		if actorID == "" {
			return nil, nil
		}
		if a, ok := actors[actorID]; ok {
			return a, nil
		}
		a, err := s.st.GetActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
		actors[actorID] = a
		return a, nil
	}

	work := model.CockpitWork{
		InProgress: []model.CockpitWorkItem{}, InReview: []model.CockpitWorkItem{},
		Ready: []model.CockpitWorkItem{}, Blocked: []model.CockpitWorkItem{},
	}
	secondary := []model.SecondaryConcern{}

	// The bucket a fact lands in is the only thing that varies here; mapping
	// it and its error plumbing are the same in all four cases.
	for _, f := range facts {
		var bucket *[]model.CockpitWorkItem
		blocked := false
		switch f.Task.State {
		case "in_progress":
			bucket = &work.InProgress
		case "in_review":
			bucket = &work.InReview
		case "ready":
			if f.Blocked() {
				bucket, blocked = &work.Blocked, true
			} else {
				bucket = &work.Ready
			}
		default:
			continue
		}
		item, err := mapWorkItem(f, blocked, resolveActor)
		if err != nil {
			return nil, err
		}
		*bucket = append(*bucket, item)
		if blocked {
			secondary = append(secondary, blockerConcerns(f)...)
		}
	}

	focus := p.Focus
	if focus == nil {
		focus = []string{}
	}

	pinnedFocus, err := buildPinnedFocus(p, resolveActor)
	if err != nil {
		return nil, err
	}

	return &model.CockpitProjection{
		CanonicalURL: "/projects/" + p.ID,
		Project:      model.CockpitProject{ID: p.ID, Name: p.Name, Key: p.Key},
		Mode: model.CockpitMode{
			Name:  string(selectMode(modeFactsForProject(*p))),
			Basis: operationsModeBasis,
		},
		PinnedFocus:       pinnedFocus,
		RankingFocus:      focus,
		NextDecision:      buildNextDecision(p),
		Work:              work,
		SecondaryConcerns: secondary,
		Repositories:      toCockpitRepositories(repos),
		Cost:              toCostReportJSON(cost),
	}, nil
}

// buildPinnedFocus maps the project's curated "Pinned focus" card (migration
// 0013): nil when no note is set, otherwise the note plus the resolved pinner.
// Only a real actor-lookup error propagates — an unknown pinner falls back to
// its raw string and never fails the whole cockpit (see pinnedBySummary).
func buildPinnedFocus(p *store.Project, resolveActor func(string) (*store.Actor, error)) (*model.Focus, error) {
	if p.FocusNote == "" {
		return nil, nil
	}
	by, err := pinnedBySummary(p.FocusPinnedBy, resolveActor)
	if err != nil {
		return nil, err
	}
	return &model.Focus{
		Note:     p.FocusNote,
		PinnedBy: by,
		PinnedAt: p.FocusPinnedAt,
	}, nil
}

// pinnedBySummary resolves a pinned-focus "pinned by" value, which may be an
// actor id or a plain display name seeded before the pinner had an actor row.
// A resolved actor yields its id and display name; a non-empty value that
// resolves to no actor falls back to the raw value as the name (so a seeded
// name still shows); an empty value yields nil. store.ErrNotFound is the "no
// such actor" signal and takes the fallback path — only some other lookup
// error propagates, so an unknown pinner never fails the whole cockpit.
func pinnedBySummary(pinnedBy string, resolveActor func(string) (*store.Actor, error)) (*model.ActorSummary, error) {
	if pinnedBy == "" {
		return nil, nil
	}
	a, err := resolveActor(pinnedBy)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &model.ActorSummary{Name: pinnedBy}, nil
		}
		return nil, err
	}
	if a == nil {
		return &model.ActorSummary{Name: pinnedBy}, nil
	}
	return &model.ActorSummary{ID: a.ID, Name: displayNameOrID(a)}, nil
}

// buildNextDecision maps the project's curated "Next decision" card (migration
// 0013): nil when no title is set, otherwise the title, who is accountable,
// and the readiness note. Subject/Actions/Evidence stay at their zero values —
// the curated v0 card carries none.
func buildNextDecision(p *store.Project) *model.Decision {
	if p.DecisionTitle == "" {
		return nil
	}
	return &model.Decision{
		Title:       p.DecisionTitle,
		Accountable: p.DecisionAccountable,
		Readiness:   p.DecisionReadiness,
	}
}

// mapWorkItem builds one cockpit work item from a project work fact. blocked
// is the bucket's fixed value the caller has already determined (true only
// for a ready task something holds — an open blocker task or an unfinished
// plan ordered before its own; see assembleProjectCockpit's switch), so no
// further lookup happens here.
//
// owner comes only from Task.Assignee: human ownership recorded through
// /assign or /start, independent of any lease. delegate comes only from an
// unreleased lease (f.Lease, already filtered by ListProjectWorkFacts to
// released_at IS NULL) whose holder is an agent actor — a human or service
// lease is real, technical evidence of who is touching the task, but is
// never surfaced as a delegate or Crew member (spec 032 §6).
func mapWorkItem(f store.ProjectWorkFact, blocked bool, resolveActor func(string) (*store.Actor, error)) (model.CockpitWorkItem, error) {
	t := f.Task

	owner, err := resolveActorSummary(t.Assignee, resolveActor)
	if err != nil {
		return model.CockpitWorkItem{}, err
	}

	var delegate *model.ActorSummary
	if f.Lease != nil {
		leaseActor, err := resolveActor(f.Lease.ActorID)
		if err != nil {
			return model.CockpitWorkItem{}, err
		}
		if leaseActor != nil && leaseActor.Kind == "agent" {
			delegate = &model.ActorSummary{ID: leaseActor.ID, Name: displayNameOrID(leaseActor)}
		}
	}

	return model.CockpitWorkItem{
		ID:             t.ID,
		Title:          t.Title,
		Priority:       t.Priority,
		State:          t.State,
		Blocked:        blocked,
		URL:            "/tasks/" + t.ID,
		Owner:          owner,
		Delegate:       delegate,
		StatusEvidence: workItemEvidence(t.State, f.StateEvent),
	}, nil
}

// workItemEvidence describes the evidence behind a work item's current
// state: a declared fact (the tracker's own state, no witnessing event) when
// the task has never transitioned, otherwise the exact source, type, event
// id, and time of the event that produced it — stateEvidence's classifying
// inputs are always retained in the human-readable summary, not thrown away.
func workItemEvidence(state string, ev *store.EventFact) model.EvidenceSummary {
	if ev == nil {
		cat := evidenceDeclared
		return model.EvidenceSummary{
			Category: string(cat),
			Summary:  fmt.Sprintf("%s: task state is %s (no recorded event)", cat.Label(), state),
		}
	}
	cat := stateEvidence(ev.Source, ev.Type, true)
	return model.EvidenceSummary{
		Category: string(cat),
		Summary: fmt.Sprintf("%s: %s event %q (id %d) at %s set state to %s",
			cat.Label(), ev.Source, ev.Type, ev.ID, ev.At.UTC().Format(time.RFC3339), state),
	}
}

// blockerConcerns turns what holds f into secondary concerns: one entry per
// open blocker task, naming the blocked task it holds up, plus one per
// unfinished plan ordered before f's own plan (025 §9.3). f is assumed to be
// blocked (f.Blocked() true) — assembleProjectCockpit only calls this for the
// ready-and-blocked case.
//
// The plan entries are not redundant with the task entries: a blocking plan
// still draft has minted no task, so it is the only thing there is to name.
func blockerConcerns(f store.ProjectWorkFact) []model.SecondaryConcern {
	out := make([]model.SecondaryConcern, 0, len(f.OpenBlockers)+len(f.BlockingPlans))
	for _, b := range f.OpenBlockers {
		out = append(out, model.SecondaryConcern{
			Kind:  "blocker",
			Title: b.Title,
			URL:   "/tasks/" + b.ID,
			Evidence: model.EvidenceSummary{
				Category: string(evidenceDeclared),
				Summary:  fmt.Sprintf("Blocks %s (blocker state %s)", f.Task.ID, b.State),
			},
		})
	}
	for _, p := range f.BlockingPlans {
		out = append(out, model.SecondaryConcern{
			Kind:  "blocker",
			Title: p.Title,
			URL:   "/docs/" + strconv.FormatInt(p.ID, 10),
			Evidence: model.EvidenceSummary{
				Category: string(evidenceDeclared),
				Summary: fmt.Sprintf("Plan %s (%s) is ordered before %s's plan and is unfinished",
					p.Slug, p.Status, f.Task.ID),
			},
		})
	}
	return out
}

// resolveActorSummary resolves id through resolveActor and, if found, builds
// a model.ActorSummary with its display name (falling back to the id when the
// actor has none). Returns nil, nil for an empty id or an actor that does
// not resolve.
func resolveActorSummary(id string, resolveActor func(string) (*store.Actor, error)) (*model.ActorSummary, error) {
	a, err := resolveActor(id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, nil
	}
	return &model.ActorSummary{ID: a.ID, Name: displayNameOrID(a)}, nil
}

// displayNameOrID returns a's display name, falling back to its id when the
// display name is empty (e.g. a service actor created without one).
func displayNameOrID(a *store.Actor) string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.ID
}

// toCockpitRepositories adapts mapped repos into cockpit repository facts:
// a project_repos row is a declared fact (someone mapped the repo and its
// done_state; no event backs it), never observed or recommended.
func toCockpitRepositories(repos []model.RepoMapping) []model.Repository {
	out := make([]model.Repository, 0, len(repos))
	for _, m := range repos {
		out = append(out, model.Repository{
			Repo:      m.Repo,
			DoneState: m.DoneState,
			StatusEvidence: model.EvidenceSummary{
				Category: string(evidenceDeclared),
				Summary:  fmt.Sprintf("Repo mapping declares done_state %s", m.DoneState),
			},
		})
	}
	return out
}

// --- HTTP handler ------------------------------------------------------

// projectCockpit handles GET /api/v1/projects/{id}/cockpit: the project
// cockpit projection (see assembleProjectCockpit).
func (s *server) projectCockpit(w http.ResponseWriter, r *http.Request) {
	proj, err := s.assembleProjectCockpit(r.Context(), r.PathValue("id"))
	s.observeCockpitProjection("api", err)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proj)
}
