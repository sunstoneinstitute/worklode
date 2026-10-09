// The branch-rules refresh loop (WL-SPEC-85 §7). The Progress page needs to
// know whether a PR's base branch runs a merge queue — it decides both the
// position line's wording and whether the merge button queues or merges — and
// it must never ask GitHub while rendering. So worklode reads the fact through
// the App on its own schedule and stores it. The same read records whether
// the branch requires pull requests, which `lode doctor` reads through
// GET /api/v1/repos/branch-rules (WL-SPEC-72 §3).

package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// branchRulesInterval is §6.3's "once a day". A ruleset change is normally
// picked up long before the next tick, by the repository_ruleset webhook
// poking the kick channel; the tick is what covers a missed delivery.
const branchRulesInterval = 24 * time.Hour

// branchRulesTimeout bounds one repo's GitHub round trips (installation
// lookup, token mint, repo read, rules read) so an unresponsive GitHub costs
// one repo's pass, not the whole loop's.
const branchRulesTimeout = 30 * time.Second

// kickBranchRules asks the loop to refresh now. Non-blocking, over a channel
// buffered to one: a kick that arrives mid-refresh is remembered and runs one
// more pass, and a burst of ruleset events collapses into that single pass.
func (s *server) kickBranchRules() {
	select {
	case s.branchRulesKick <- struct{}{}:
	default:
	}
}

// branchRulesLoop refreshes on start, then every branchRulesInterval or
// whenever kickBranchRules fires. NewServer starts it only when a
// BackgroundCtx and a GitHub App are both configured.
func (s *server) branchRulesLoop(ctx context.Context) {
	t := time.NewTicker(branchRulesInterval)
	defer t.Stop()
	for {
		s.refreshBranchRules(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.branchRulesKick:
		}
	}
}

// refreshBranchRules re-reads every mapped repo's default-branch rules.
//
// A repo whose read fails keeps whatever was known before — nothing, on an
// instance with no App configured. Readers treat unknown as "no queue", so a
// GitHub outage degrades the merge button to the plain-merge wording rather
// than writing a fact worklode did not observe.
func (s *server) refreshBranchRules(ctx context.Context) {
	if s.appAuth == nil {
		return
	}
	repos, err := s.st.MappedRepos(ctx)
	if err != nil {
		s.log.Error("branch rules: list mapped repos", "err", err)
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		if err := s.refreshRepoBranchRules(ctx, repo); err != nil {
			s.log.Warn("branch rules refresh failed", "repo", repo, "err", err)
		}
	}
}

// refreshRepoBranchRules records whether one repo's default branch runs a
// merge queue and requires pull requests.
func (s *server) refreshRepoBranchRules(ctx context.Context, repo string) error {
	ctx, cancel := context.WithTimeout(ctx, branchRulesTimeout)
	defer cancel()

	rc, err := s.appAuth.NewRepoClient(ctx, repo)
	if err != nil {
		return err
	}
	branch, err := rc.DefaultBranch(ctx)
	if err != nil {
		return err
	}
	s.observeGitHubCall("branch_rules")
	mergeQueue, pullRequest, err := s.appAuth.BranchRules(ctx, repo, branch)
	if err != nil {
		return err
	}
	now := s.st.Now()
	return s.st.Tx(ctx, func(tx *sql.Tx) error {
		return store.UpsertBranchRules(tx, repo, branch, mergeQueue, pullRequest, now)
	})
}

// getBranchRules handles GET /api/v1/repos/branch-rules?remote=<url>: the
// stored branch rules for the repo the remote names. 404 when nothing has
// been observed for it, so a caller can say "unknown" rather than guess.
func (s *server) getBranchRules(w http.ResponseWriter, r *http.Request) {
	var params model.RepoBranchRulesParams
	if err := readQuery(r, &params); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	repo, err := repourl.Normalize(params.Remote)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rules, err := s.st.BranchRules(r.Context(), repo)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// branchrulesRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var branchrulesRouteDocs = map[string]routeDoc{
	"GET /api/v1/repos/branch-rules": {
		summary:   "Get the rules last observed on a repo's default branch",
		responses: map[int]any{http.StatusOK: model.RepoBranchRules{}},
		params:    model.RepoBranchRulesParams{},
	},
}
