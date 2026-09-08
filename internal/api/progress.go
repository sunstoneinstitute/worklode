// progress.go serves the project Progress page (WL-SPEC-66 §2): one bulk
// read of the project's corpus and task set (store.ProjectProgress), derived
// into model.ProjectProgress by internal/progress, rendered by ui.Progress.
// Nothing is stored and nothing is cached — every state on the page is
// recomputed per request, so it can never disagree with the run board or
// `lode doc todo` (§1).
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/progress"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// progressPage handles GET /projects/{id}/progress, runBoardPage-shaped: the
// project header first (so an unknown project 404s like every other project
// route), then the one progress read.
//
// A project with no spec has no Progress page at all (§2, amending 056 §2):
// the sidebar shows no entry and the route answers 404. The specs in the
// reader's own input decide that, so the page and the sidebar cannot disagree
// about whether the project has any.
func (s *server) progressPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	in, err := s.st.ProjectProgress(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	if len(in.Specs) == 0 {
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	p := progress.Derive(in)
	s.renderWeb(w, r, http.StatusOK, "progress page", ui.Progress(ui.ProgressView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Progress"},
		CanonicalURL: "/projects/" + project.ID + "/progress",
		Project:      project,
		Viewer:       actorIDFrom(r),
		P:            p,
		Legend:       ui.ProgressLegend(p),
	}))
}

// hasSpecs is the sidebar's Progress-entry bit. It is read here rather than
// carried on model.CockpitProject because the JSON cockpit's shape is
// contracted by spec 032 and this is a page affordance, not a change to that
// contract — the same reason projectPage reads its agent sessions and rally
// off the store. A failed read logs and hides the entry: the page it leads to
// would fail the same way.
func (s *server) hasSpecs(ctx context.Context, projectID string) bool {
	has, err := s.st.ProjectHasSpecs(ctx, projectID)
	if err != nil {
		s.log.Error("check project specs", "err", err, "project", projectID)
		return false
	}
	return has
}

// getProjectProgress handles GET /api/v1/projects/{id}/progress: the same
// derived model.ProjectProgress the page renders, as JSON, so an agent reads
// what a person sees (066 §7). The project is read first so an unknown id
// 404s rather than answering with an empty corpus. A project with no spec is
// not a 404 here — the page hides itself because there is nothing to draw,
// but "this project has no specs" is a real answer to an API question.
func (s *server) getProjectProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(ctx, projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	in, err := s.st.ProjectProgress(ctx, projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, progress.Derive(in))
}

// --- writes (WL-SPEC-66 §3) -------------------------------------------------

// progressAccept handles POST /projects/{id}/progress/accept (§3.2). It
// performs exactly what POST /api/v1/docs/{id}/accept performs — the same
// typed event, the same store.AcceptDoc — so the owner gate (025 §7) and, for
// a plan, minting its tasks in the same transaction (025 §9.2) are the
// store's. This page can never accept what `lode doc accept` would refuse.
//
// Two refusals are the route's own. A document of another project is a 404:
// the route is project-scoped, whatever the caller may do with that document
// elsewhere. A document that is not draft is a 409, because draft is the
// state the button is offered on (§3.2) and a plan is otherwise re-acceptable
// while accepted, which would turn a stale page's second click into a silent
// no-op reported as success. Neither discloses anything the viewer did not
// just read off the page.
func (s *server) progressAccept(w http.ResponseWriter, r *http.Request) {
	var body model.ProgressAcceptInput
	sub, project, ok := s.beginJSONPost(w, r, "accept", &body)
	if !ok {
		return
	}
	ctx := r.Context()

	doc, err := s.st.GetDoc(ctx, body.Doc)
	if err != nil {
		s.observeProgressWrite("accept", progressAcceptOutcome(err))
		s.mapStoreErr(w, err)
		return
	}
	if doc.Project != project.ID {
		s.observeProgressWrite("accept", "refused")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if doc.Status != "draft" {
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, fmt.Sprintf("doc %d is %s, not draft", doc.ID, doc.Status))
		return
	}

	accepted, minted, inserted, err := s.emitDocAccepted(ctx, doc, sub.ActorID)
	switch {
	// The backbone refused the state, not the actor: the depth gate, a plan
	// that will not parse, or a document that left draft between the check
	// above and the row lock. §4.2 rule 4 makes that a 409 here rather than
	// mapStoreErr's 422, which on this route means a malformed body.
	case errors.Is(err, store.ErrInvalidInput):
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		s.observeProgressWrite("accept", progressAcceptOutcome(err))
		s.mapStoreErr(w, err)
	case !inserted:
		// Someone else accepted this document at this version while the
		// request was in flight, so nothing was applied here. Reporting it as
		// a success would credit this actor with an accept the owner gate
		// never admitted (§4.2 rule 7).
		s.observeProgressWrite("accept", "conflict")
		writeErr(w, http.StatusConflict, fmt.Sprintf("doc %d was already accepted", doc.ID))
	default:
		s.st.RecordPlanTasksMinted(len(minted))
		s.observeProgressWrite("accept", "ok")
		writeJSON(w, http.StatusOK, model.ProgressAcceptResponse{
			Doc: accepted.ID, Status: accepted.Status, Minted: len(minted),
		})
	}
}

// progressAcceptOutcome bounds a store error to one of the write metric's
// outcomes: "refused" is the act declined — an unknown document, an actor
// who is not the owner — and "error" is a fault.
func progressAcceptOutcome(err error) string {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		return "refused"
	}
	return "error"
}
