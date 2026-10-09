// Branch rules: whether a branch is protected by a merge queue
// (WL-SPEC-82 §15.6) and whether it requires pull requests (WL-SPEC-72 §3). Read through an installation token, like every other
// per-repo fact in this package.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// BranchRules reports whether branch in repo ("owner/name") is covered by a
// merge-queue rule and by a pull-request rule. GET
// /repos/{repo}/rules/branches/{branch} lists the rules in force on that
// branch, flattened across every ruleset that applies. Classic branch
// protection is not part of that list.
//
// A branch with no rulesets answers with an empty array — a fact ("no
// queue, no PR rule"), not an error. A transport failure or a non-200 is an error, so a
// caller never records "no queue" because GitHub was unreachable.
func (a *AppAuth) BranchRules(ctx context.Context, repo, branch string) (mergeQueue, pullRequest bool, err error) {
	path, err := repoPath(repo)
	if err != nil {
		return false, false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return false, false, err
	}
	var rules []struct {
		Type string `json:"type"`
	}
	rulesURL := a.BaseURL + "/repos/" + path + "/rules/branches/" + url.PathEscape(branch)
	code, err := githubJSON(ctx, http.MethodGet, rulesURL, "Bearer "+token, &rules)
	if err != nil {
		return false, false, err
	}
	if code != http.StatusOK {
		return false, false, fmt.Errorf("branch rules %s#%s: status %d", repo, branch, code)
	}
	for _, r := range rules {
		switch r.Type {
		case "merge_queue":
			mergeQueue = true
		case "pull_request":
			pullRequest = true
		}
	}
	return mergeQueue, pullRequest, nil
}
