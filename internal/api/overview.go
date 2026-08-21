// overview.go serves spec 007's read surface — the roll-up, drift, gaps, the
// frontier mirror and the critical path — plus the on-demand run of the
// server-side derivers. The reads are computed in internal/overview; this
// file is the thin HTTP skin over it: parse the query, map the one domain
// error the service has, record the read, serialize.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/overview"
)

// failedOverviewRead records one overview read and, when it failed, writes
// the mapped refusal. It returns true when the handler must stop. An
// unconfigured graph is 503 (the deployment lacks the endpoint, the request
// was fine); anything else is a 500.
func (s *server) failedOverviewRead(w http.ResponseWriter, read string, err error) bool {
	s.observeOverviewRead(read, overviewOutcome(err))
	switch {
	case err == nil:
		return false
	case errors.Is(err, overview.ErrNoGraph):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
	return true
}

// getOverview handles GET /api/v1/overview?project=<id>.
func (s *server) getOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.overview.Roll(r.Context(), r.URL.Query().Get("project"))
	if s.failedOverviewRead(w, readOverview, err) {
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// queryFlag reads a boolean query parameter the way a caller writes one: an
// absent parameter is false, a bare `?flag` is true, and only an explicit
// false value turns it off. Get alone reads `?flag` as false and `?flag=0` as
// true, which is backwards on both.
func queryFlag(r *http.Request, name string) bool {
	q := r.URL.Query()
	if !q.Has(name) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(q.Get(name))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// getDrift handles GET /api/v1/drift?acknowledged=1.
func (s *server) getDrift(w http.ResponseWriter, r *http.Request) {
	d, err := s.overview.DriftReport(r.Context(), queryFlag(r, "acknowledged"))
	if s.failedOverviewRead(w, readDrift, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// getGaps handles GET /api/v1/gaps.
func (s *server) getGaps(w http.ResponseWriter, r *http.Request) {
	g, err := s.overview.GapReport(r.Context())
	if s.failedOverviewRead(w, readGaps, err) {
		return
	}
	writeJSON(w, http.StatusOK, model.GapList{Gaps: g})
}

// getFrontier handles GET /api/v1/frontier?project=<id> — the read-only
// mirror of the backbone frontier, pre-sorted by the D9 key.
func (s *server) getFrontier(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.overview.Frontier(r.Context(), r.URL.Query().Get("project"))
	if s.failedOverviewRead(w, readFrontier, err) {
		return
	}
	writeJSON(w, http.StatusOK, model.FrontierList{Tasks: tasks})
}

// getCriticalPath handles GET /api/v1/critical-path.
func (s *server) getCriticalPath(w http.ResponseWriter, r *http.Request) {
	cp, err := s.overview.CriticalPath(r.Context())
	if s.failedOverviewRead(w, readCriticalPath, err) {
		return
	}
	writeJSON(w, http.StatusOK, cp)
}

// postDerive handles POST /api/v1/derive: run the server-side derivers
// (deploy, pr-affects) on demand. Admin-gated — it replaces org-wide named
// graphs and spends GitHub App API calls across every repo.
func (s *server) postDerive(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Graph == nil {
		writeErr(w, http.StatusServiceUnavailable, overview.ErrNoGraph.Error())
		return
	}
	if s.appAuth == nil {
		writeErr(w, http.StatusServiceUnavailable,
			"the GitHub App is not configured (LODE_GITHUB_APP_ID, "+
				"LODE_GITHUB_APP_PRIVATE_KEY): the pr-affects deriver reads "+
				"every task PR's files through it")
		return
	}
	results, err := s.runServerDerivers(r.Context())
	if err != nil {
		writeErr(w, deriveFailureStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.DeriveResponse{Results: results})
}

// deriveFailureStatus names the party a failed deriver run blames. The run
// reads Postgres (deployment rows, task PRs) as well as writing the graph
// endpoint, and a store failure reported as 502 sends the operator to the
// wrong service: 502 is for the upstream graph endpoint, 500 for us.
// graphserver.ErrSPARQLUnavailable wraps ErrUnavailable, so the one check
// covers both faces of it.
func deriveFailureStatus(err error) int {
	if errors.Is(err, graphserver.ErrUnavailable) {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// runServerDerivers runs the two derivers that need the server's own inputs:
// deploy (the store's deployment rows) and pr-affects (every task-bound PR's
// files, read through the GitHub App). The repo-local derivers run from a
// checkout instead — see `lode derive`.
//
// Callers must have established that both s.cfg.Graph and s.appAuth are
// configured; postDerive is the only one and refuses with 503 otherwise. A
// partial run returns what landed alongside the error, so an operator sees
// which graph was replaced before the failure.
func (s *server) runServerDerivers(ctx context.Context) ([]model.DeriveResult, error) {
	var out []model.DeriveResult
	doc, err := derive.DeployTriples(ctx, s.st)
	if err != nil {
		s.observeDeriveRun(deriveDeploy, deriveErrored)
		return out, err
	}
	// AllowEmpty: false. The deploy document can only be empty if
	// the deriver itself broke: it opens with the fixed environment
	// vocabulary (graphproj.EnvironmentTriples), which depends on no
	// row and is never empty, so "no triples" here means the deriver
	// stopped producing, not that the estate is empty.
	res, err := derive.Run(ctx, s.cfg.Graph, iri.ObservedGraph("deploy"), doc,
		derive.Options{})
	s.observeDeriveRun(deriveDeploy, deriveOutcome(res, err))
	if err != nil {
		return out, err
	}
	out = append(out, res)

	prs, err := s.st.TaskPRs(ctx)
	if err != nil {
		s.observeDeriveRun(derivePRAffects, deriveErrored)
		return out, err
	}
	doc, skipped, err := derive.PRAffectsTriples(ctx, prs, s.repoReader)
	if err != nil {
		s.observeDeriveRun(derivePRAffects, deriveErrored)
		return out, err
	}
	// Never discarded: a repo with no manifest, or one the GitHub App
	// is not installed on, is skipped rather than fatal, so an
	// org-wide manifest or installation outage is otherwise silent
	// until the guard below turns it into an opaque
	// ErrWouldEmptyGraph. The local derivers report their non-fatal
	// skips in `lode derive`'s output (runDeriveLocal's `notes`);
	// this is the server-side equivalent — logged for the operator,
	// and named in the error the admin's POST comes back with.
	if len(skipped) > 0 {
		s.log.Warn("pr-affects deriver skipped repos",
			"count", len(skipped), "repos", skipped)
	}
	// AllowEmpty: false. Unlike go-imports — legitimately empty in a
	// single-component repo, where every import edge is intra-
	// component by construction (WL-268) — pr-affects has no such
	// structural reason to be empty: it emits an edge for every
	// task-bound PR touching a manifest-matched path, and the
	// backbone has many. Empty means the inputs went away (manifests
	// unreadable, App uninstalled, TaskPRs returning nothing), which
	// is precisely the case that must not silently replace the graph.
	res, err = derive.Run(ctx, s.cfg.Graph, iri.ObservedGraph("pr-affects"), doc,
		derive.Options{})
	s.observeDeriveRun(derivePRAffects, deriveOutcome(res, err))
	if err != nil {
		if len(skipped) > 0 {
			return out, fmt.Errorf("%w (repos skipped this run: %s)",
				err, strings.Join(skipped, ", "))
		}
		return out, err
	}
	return append(out, res), nil
}
