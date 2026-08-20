package api

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// maxReportedMergeTasks bounds one report. A merge lands the work of a
// handful of tasks; a request naming hundreds is a client bug, and the cap
// keeps one malformed report from writing an unbounded number of rows inside
// a single transaction.
const maxReportedMergeTasks = 100

// shaPattern is what a git object id looks like. Abbreviations are accepted
// down to git's own minimum of 4, because a caller reading `git rev-parse`
// output has whatever length that repo's core.abbrev produced.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// reportMerge handles POST /api/v1/merges: the local reporter's half of
// delivery. `lode hook post-merge` sees a merge land on the default branch in
// someone's own clone and names the tasks whose branches it carries; this
// records the same three facts the default-branch push webhook records
// (store.RecordLocalMerge), so the two reporters produce one fact under one
// rule.
//
// It is deliberately not idempotent at the event level: the event row is
// unique per request, and the dedup lives in the facts (main_commits and
// task_commits both ON CONFLICT DO NOTHING). A repeat report is therefore
// recorded, costs nothing, and answers "duplicate" — which is the healthy
// steady state when both a webhook and a laptop report the same merge.
func (s *server) reportMerge(w http.ResponseWriter, r *http.Request) {
	var req model.MergeReportRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	repo, err := repourl.Normalize(req.Repo)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sha := strings.ToLower(strings.TrimSpace(req.SHA))
	if !shaPattern.MatchString(sha) {
		writeErr(w, http.StatusUnprocessableEntity, "sha must be a hex git object id")
		return
	}
	if len(req.Tasks) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "tasks is required")
		return
	}
	if len(req.Tasks) > maxReportedMergeTasks {
		writeErr(w, http.StatusUnprocessableEntity, "too many tasks in one merge report")
		return
	}

	var outcomes []store.LocalMergeOutcome
	err = s.recordEvent(r.Context(), "cli", "merge.local",
		map[string]any{"repo": repo, "sha": sha, "tasks": req.Tasks},
		func(tx *sql.Tx, eventID int64) error {
			var err error
			outcomes, err = store.RecordLocalMerge(tx, s.st.Now(), repo, sha, req.Tasks, eventID)
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	results := make([]model.MergeResult, 0, len(outcomes))
	for _, o := range outcomes {
		s.recordLocalMerge(o.Result)
		results = append(results, model.MergeResult{Task: o.TaskID, Result: o.Result})
	}
	writeJSON(w, http.StatusOK, model.MergeReport{Repo: repo, SHA: sha, Results: results})
}
