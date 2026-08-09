// cockpit.go implements the project cockpit: GET /api/v1/projects/{id}/cockpit
// and the assembler shared with the project web page (see projectPage in
// web.go). The cockpit is a projection, never a stored workflow field —
// selectMode is a pure function of declared facts, and assembleProjectCockpit
// builds its output fresh from existing store readers on every call (spec
// 032). It is provisional in Part 1: work items and repositories are adapted
// from the existing board and repo-mapping readers rather than governed
// decision/pin/secondary-concern stores, which do not exist yet. assembleBoard
// itself now reads through the shared, UI-neutral store.ListProjectWorkFacts
// (Task 3); Task 4 replaces this file's own board adapter (toCockpitWorkItems)
// with a direct, product-language mapping over those same facts, while
// preserving this wire contract.
package api

import (
	"context"
	"fmt"
	"net/http"
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

// --- assembly --------------------------------------------------------------

// assembleProjectCockpit builds the cockpit projection for one project, fresh
// on every call from GetProject, the scoped board, ListRepos, and
// ProjectCost. Returns store.ErrNotFound (via GetProject) when the project
// does not exist.
func (s *server) assembleProjectCockpit(ctx context.Context, id string) (*cockpitProjection, error) {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		return nil, err
	}
	board, err := s.assembleBoard(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := s.st.ListRepos(ctx, id)
	if err != nil {
		return nil, err
	}
	cost, err := s.st.ProjectCost(ctx, id, time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}

	focus := p.Focus
	if focus == nil {
		focus = []string{}
	}

	bp := board.Projects[0]

	return &cockpitProjection{
		CanonicalURL: "/projects/" + p.ID,
		Project:      cockpitProjectJSON{ID: p.ID, Name: p.Name, Key: p.Key},
		Mode: cockpitModeJSON{
			Name:  selectMode(modeFactsForProject(*p)),
			Basis: operationsModeBasis,
		},
		PinnedFocus:  nil,
		RankingFocus: focus,
		NextDecision: nil,
		Work: cockpitWork{
			InProgress: toCockpitWorkItems(bp.InProgress, false),
			InReview:   toCockpitWorkItems(bp.InReview, false),
			Ready:      toCockpitWorkItems(bp.Ready, false),
			Blocked:    toCockpitWorkItems(bp.Blocked, true),
		},
		SecondaryConcerns: []secondaryConcernJSON{},
		Repositories:      toCockpitRepositories(repos),
		Cost:              toProjectCostJSON(cost),
	}, nil
}

// toCockpitWorkItems adapts one board bucket into cockpit work items. blocked
// is the bucket's fixed value (true only for the Blocked bucket —
// assembleBoard has already resolved blocking edges to decide bucket
// membership, so no further lookup is needed here). Owner/Delegate carry only
// the actor id available from the board (no display-name lookup yet, hence
// Name duplicates ID); status_evidence is "declared" because task state comes
// from the tracker's own state machine, not an external observation.
func toCockpitWorkItems(tasks []boardTaskJSON, blocked bool) []cockpitWorkItem {
	out := make([]cockpitWorkItem, 0, len(tasks))
	for _, t := range tasks {
		item := cockpitWorkItem{
			ID:       t.ID,
			Title:    t.Title,
			Priority: t.Priority,
			State:    t.State,
			Blocked:  blocked,
			URL:      "/tasks/" + t.ID,
			StatusEvidence: evidenceSummary{
				Category: "declared",
				Summary:  fmt.Sprintf("Task state is %s", t.State),
			},
		}
		if t.Holder != nil {
			item.Owner = &actorSummary{ID: t.Holder.ActorID, Name: t.Holder.ActorID}
		}
		if t.Assignee != "" {
			item.Delegate = &actorSummary{ID: t.Assignee, Name: t.Assignee}
		}
		out = append(out, item)
	}
	return out
}

// toCockpitRepositories adapts mapped repos into cockpit repository facts.
func toCockpitRepositories(repos []store.RepoMapping) []repositoryJSON {
	out := make([]repositoryJSON, 0, len(repos))
	for _, m := range repos {
		out = append(out, repositoryJSON{
			Repo:      m.Repo,
			DoneState: m.DoneState,
			StatusEvidence: evidenceSummary{
				Category: "declared",
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
