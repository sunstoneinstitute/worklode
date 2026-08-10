// cockpit.go implements the project cockpit: GET /api/v1/projects/{id}/cockpit
// and the assembler shared with the project web page (see projectPage in
// web.go). The cockpit is a projection, never a stored workflow field —
// selectMode is a pure function of declared facts, and assembleProjectCockpit
// builds its output fresh from existing store readers on every call (spec
// 032). Work items, owner/delegate, evidence, blockers, repositories, and
// cost are all mapped directly from store.ListProjectWorkFacts (Task 3),
// (*store.Store).GetActor, ListRepos, and ProjectCost — no board adapter, no
// invented state. Pinned focus and the next governed decision stay nil until
// Part 2 supplies the stores that back them.
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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

// --- projection shape ------------------------------------------------------

type evidenceSummary struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

type cockpitModeJSON struct {
	Name  cockpitMode     `json:"name"`
	Basis evidenceSummary `json:"basis"`
}

type cockpitProjectJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

type actorSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type focusJSON struct {
	ObjectType string        `json:"object_type"`
	ObjectID   string        `json:"object_id"`
	Note       string        `json:"note"`
	PinnedBy   *actorSummary `json:"pinned_by"`
	PinnedAt   time.Time     `json:"pinned_at"`
}

type decisionActionJSON struct {
	Label  string `json:"label"`
	Effect string `json:"effect"`
	URL    string `json:"url"`
}

type evidenceReferenceJSON struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Category string `json:"category"`
}

type decisionJSON struct {
	Title            string                  `json:"title"`
	Accountable      string                  `json:"accountable"`
	Subject          string                  `json:"subject"`
	Readiness        string                  `json:"readiness"`
	Actions          []decisionActionJSON    `json:"actions"`
	Evidence         []evidenceReferenceJSON `json:"evidence"`
	ContraryEvidence []evidenceReferenceJSON `json:"contrary_evidence"`
}

type secondaryConcernJSON struct {
	Kind     string          `json:"kind"`
	Title    string          `json:"title"`
	URL      string          `json:"url"`
	Evidence evidenceSummary `json:"evidence"`
}

type repositoryJSON struct {
	Repo           string          `json:"repo"`
	DoneState      string          `json:"done_state"`
	StatusEvidence evidenceSummary `json:"status_evidence"`
}

type cockpitWorkItem struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Priority       string          `json:"priority"`
	State          string          `json:"state"`
	Blocked        bool            `json:"blocked"`
	URL            string          `json:"url"`
	Owner          *actorSummary   `json:"owner"`
	Delegate       *actorSummary   `json:"delegate"`
	StatusEvidence evidenceSummary `json:"status_evidence"`
}

type cockpitWork struct {
	InProgress []cockpitWorkItem `json:"in_progress"`
	InReview   []cockpitWorkItem `json:"in_review"`
	Ready      []cockpitWorkItem `json:"ready"`
	Blocked    []cockpitWorkItem `json:"blocked"`
}

type cockpitProjection struct {
	CanonicalURL      string                 `json:"canonical_url"`
	Project           cockpitProjectJSON     `json:"project"`
	Mode              cockpitModeJSON        `json:"mode"`
	PinnedFocus       *focusJSON             `json:"pinned_focus"`
	RankingFocus      []string               `json:"ranking_focus"`
	NextDecision      *decisionJSON          `json:"next_decision"`
	Work              cockpitWork            `json:"work"`
	SecondaryConcerns []secondaryConcernJSON `json:"secondary_concerns"`
	Repositories      []repositoryJSON       `json:"repositories"`
	Cost              projectCostJSON        `json:"cost"`
}

// operationsModeBasis is the fixed evidence basis for modeOperations, the
// only mode selectMode can produce until Part 2 stores promotion/Enter
// Research facts (see modeFactsForProject).
var operationsModeBasis = evidenceSummary{
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
func (s *server) assembleProjectCockpit(ctx context.Context, id string) (*cockpitProjection, error) {
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

	work := cockpitWork{
		InProgress: []cockpitWorkItem{}, InReview: []cockpitWorkItem{},
		Ready: []cockpitWorkItem{}, Blocked: []cockpitWorkItem{},
	}
	secondary := []secondaryConcernJSON{}

	for _, f := range facts {
		switch {
		case f.Task.State == "in_progress":
			item, err := mapWorkItem(f, false, resolveActor)
			if err != nil {
				return nil, err
			}
			work.InProgress = append(work.InProgress, item)
		case f.Task.State == "in_review":
			item, err := mapWorkItem(f, false, resolveActor)
			if err != nil {
				return nil, err
			}
			work.InReview = append(work.InReview, item)
		case f.Task.State == "ready" && f.Blocked():
			item, err := mapWorkItem(f, true, resolveActor)
			if err != nil {
				return nil, err
			}
			work.Blocked = append(work.Blocked, item)
			secondary = append(secondary, blockerConcerns(f)...)
		case f.Task.State == "ready":
			item, err := mapWorkItem(f, false, resolveActor)
			if err != nil {
				return nil, err
			}
			work.Ready = append(work.Ready, item)
		}
	}

	focus := p.Focus
	if focus == nil {
		focus = []string{}
	}

	return &cockpitProjection{
		CanonicalURL: "/projects/" + p.ID,
		Project:      cockpitProjectJSON{ID: p.ID, Name: p.Name, Key: p.Key},
		Mode: cockpitModeJSON{
			Name:  selectMode(modeFactsForProject(*p)),
			Basis: operationsModeBasis,
		},
		PinnedFocus:       nil,
		RankingFocus:      focus,
		NextDecision:      nil,
		Work:              work,
		SecondaryConcerns: secondary,
		Repositories:      toCockpitRepositories(repos),
		Cost:              toProjectCostJSON(cost),
	}, nil
}

// mapWorkItem builds one cockpit work item from a project work fact. blocked
// is the bucket's fixed value the caller has already determined (true only
// for a ready task with an open blocker — see assembleProjectCockpit's
// switch), so no further lookup happens here.
//
// owner comes only from Task.Assignee: human ownership recorded through
// /assign or /start, independent of any lease. delegate comes only from an
// unreleased lease (f.Lease, already filtered by ListProjectWorkFacts to
// released_at IS NULL) whose holder is an agent actor — a human or service
// lease is real, technical evidence of who is touching the task, but is
// never surfaced as a delegate or Crew member (spec 032 §6).
func mapWorkItem(f store.ProjectWorkFact, blocked bool, resolveActor func(string) (*store.Actor, error)) (cockpitWorkItem, error) {
	t := f.Task

	owner, err := resolveActorSummary(t.Assignee, resolveActor)
	if err != nil {
		return cockpitWorkItem{}, err
	}

	var delegate *actorSummary
	if f.Lease != nil {
		leaseActor, err := resolveActor(f.Lease.ActorID)
		if err != nil {
			return cockpitWorkItem{}, err
		}
		if leaseActor != nil && leaseActor.Kind == "agent" {
			delegate = &actorSummary{ID: leaseActor.ID, Name: displayNameOrID(leaseActor)}
		}
	}

	return cockpitWorkItem{
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
func workItemEvidence(state string, ev *store.EventFact) evidenceSummary {
	if ev == nil {
		cat := evidenceDeclared
		return evidenceSummary{
			Category: string(cat),
			Summary:  fmt.Sprintf("%s: task state is %s (no recorded event)", cat.Label(), state),
		}
	}
	cat := stateEvidence(ev.Source, ev.Type, true)
	return evidenceSummary{
		Category: string(cat),
		Summary: fmt.Sprintf("%s: %s event %q (id %d) at %s set state to %s",
			cat.Label(), ev.Source, ev.Type, ev.ID, ev.At.UTC().Format(time.RFC3339), state),
	}
}

// blockerConcerns turns f's open blocker refs into secondary concerns: one
// entry per open blocker, naming the blocked task it holds up. f is assumed
// to be blocked (f.Blocked() true) — assembleProjectCockpit only calls this
// for the ready-and-blocked case.
func blockerConcerns(f store.ProjectWorkFact) []secondaryConcernJSON {
	out := make([]secondaryConcernJSON, 0, len(f.OpenBlockers))
	for _, b := range f.OpenBlockers {
		out = append(out, secondaryConcernJSON{
			Kind:  "blocker",
			Title: b.Title,
			URL:   "/tasks/" + b.ID,
			Evidence: evidenceSummary{
				Category: string(evidenceDeclared),
				Summary:  fmt.Sprintf("Blocks %s (blocker state %s)", f.Task.ID, b.State),
			},
		})
	}
	return out
}

// resolveActorSummary resolves id through resolveActor and, if found, builds
// an actorSummary with its display name (falling back to the id when the
// actor has none). Returns nil, nil for an empty id or an actor that does
// not resolve.
func resolveActorSummary(id string, resolveActor func(string) (*store.Actor, error)) (*actorSummary, error) {
	a, err := resolveActor(id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, nil
	}
	return &actorSummary{ID: a.ID, Name: displayNameOrID(a)}, nil
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
func toCockpitRepositories(repos []store.RepoMapping) []repositoryJSON {
	out := make([]repositoryJSON, 0, len(repos))
	for _, m := range repos {
		out = append(out, repositoryJSON{
			Repo:      m.Repo,
			DoneState: m.DoneState,
			StatusEvidence: evidenceSummary{
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
