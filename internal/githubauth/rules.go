// Branch rules: whether a branch is protected by a merge queue
// (WL-SPEC-66 §6.3). Read through an installation token, like every other
// per-repo fact in this package.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// BranchRules reports whether branch in repo ("owner/name") is covered by a
// merge-queue rule. GET /repos/{repo}/rules/branches/{branch} lists the rules
// in force on that branch, flattened across every ruleset that applies; a rule
// of type "merge_queue" is the queue.
//
// A branch with no rulesets answers with an empty array — a fact ("no
// queue"), not an error. A transport failure or a non-200 is an error, so a
// caller never records "no queue" because GitHub was unreachable.
func (a *AppAuth) BranchRules(ctx context.Context, repo, branch string) (mergeQueue bool, err error) {
	path, err := repoPath(repo)
	if err != nil {
		return false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return false, err
	}
	var rules []struct {
		Type string `json:"type"`
	}
	rulesURL := a.BaseURL + "/repos/" + path + "/rules/branches/" + url.PathEscape(branch)
	code, err := githubJSON(ctx, http.MethodGet, rulesURL, "Bearer "+token, &rules)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, fmt.Errorf("branch rules %s#%s: status %d", repo, branch, code)
	}
	for _, r := range rules {
		if r.Type == "merge_queue" {
			return true, nil
		}
	}
	return false, nil
}
