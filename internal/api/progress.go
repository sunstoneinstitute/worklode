// progress.go serves the project Progress page (WL-SPEC-66 §2): one bulk
// read of the project's corpus and task set (store.ProjectProgress), derived
// into model.ProjectProgress by internal/progress, rendered by ui.Progress.
// Nothing is stored and nothing is cached — every state on the page is
// recomputed per request, so it can never disagree with the run board or
// `lode doc todo` (§1).
package api

import (
	"context"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/progress"
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
