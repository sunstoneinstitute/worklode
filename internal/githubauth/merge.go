// Acting on a pull request (WL-SPEC-66 §3.6): enqueue it into the
// repository's merge queue, or merge it outright. Both act as the App's
// installation, like every other per-repo write in this package.
//
// A refusal GitHub explained is a *GitHubError carrying its message, because
// the Progress page shows that message to the person who pressed the button.

package githubauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GitHubError is a status GitHub returned with its own explanation of it.
// Status is the HTTP status, or — for a GraphQL mutation, which explains a
// refusal in a 200's body — 422.
type GitHubError struct {
	Status  int
	Message string
}

func (e *GitHubError) Error() string {
	return fmt.Sprintf("github: status %d: %s", e.Status, e.Message)
}

// maxGitHubBody bounds what a reply may cost us in memory. Every reply these
// calls read is a small JSON object.
const maxGitHubBody = 1 << 20

// githubCall performs an authenticated request, optionally carrying in as a
// JSON body, and decodes a 2xx into out. Anything else becomes a
// *GitHubError with GitHub's message. githubJSON cannot serve here: it sends
// no body and drops the body of a non-2xx, which is exactly the text §3.6
// passes back.
func githubCall(ctx context.Context, method, url, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode github request %s %s: %w", method, url, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build github request %s %s: %w", method, url, err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubBody))
	if err != nil {
		return fmt.Errorf("read github %s %s: %w", method, url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &GitHubError{Status: resp.StatusCode, Message: githubMessage(raw, resp.Status)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode github %s %s: %w", method, url, err)
		}
	}
	return nil
}

// githubMessage is the "message" field of a GitHub error body, or the status
// line when the body does not carry one.
func githubMessage(raw []byte, fallback string) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return fallback
}

// prURL is the REST URL of one pull request in repo ("owner/name").
func (a *AppAuth) prURL(repo string, number int64) (string, error) {
	path, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/repos/%s/pulls/%d", a.BaseURL, path, number), nil
}

// PRNodeID returns a pull request's GraphQL node id, which is what
// enqueuePullRequest takes and the REST payload is the only place to get.
func (a *AppAuth) PRNodeID(ctx context.Context, repo string, number int64) (string, error) {
	url, err := a.prURL(repo, number)
	if err != nil {
		return "", err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return "", err
	}
	var pr struct {
		NodeID string `json:"node_id"`
	}
	if err := githubCall(ctx, http.MethodGet, url, "Bearer "+token, nil, &pr); err != nil {
		return "", err
	}
	if pr.NodeID == "" {
		return "", fmt.Errorf("pull request %s#%d: no node_id", repo, number)
	}
	return pr.NodeID, nil
}

// EnqueuePR adds a pull request to its repository's merge queue. GraphQL,
// not REST: GitHub refuses a REST merge on a queue-protected branch, and
// enqueuePullRequest is the only API that queues one.
//
// A GraphQL refusal arrives as a 200 with an errors array. It is a refusal
// all the same, so it comes back as a *GitHubError the merge route maps to a
// 409 like any other 4xx.
func (a *AppAuth) EnqueuePR(ctx context.Context, repo, nodeID string) error {
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return err
	}
	in := map[string]any{
		"query":     `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){clientMutationId}}`,
		"variables": map[string]any{"id": nodeID},
	}
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := githubCall(ctx, http.MethodPost, a.BaseURL+"/graphql", "Bearer "+token, in, &out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return &GitHubError{Status: http.StatusUnprocessableEntity, Message: out.Errors[0].Message}
	}
	return nil
}

// MergePR merges a pull request through the REST merge endpoint. An empty
// method leaves the choice to GitHub, which uses the repository's default.
//
// ponytail: worklode does not read which merge methods the repository
// allows; a repository that forbids the default answers with its own message
// and the page shows it. Read allow_squash_merge and friends here if that
// refusal ever becomes routine.
func (a *AppAuth) MergePR(ctx context.Context, repo string, number int64, method string) error {
	url, err := a.prURL(repo, number)
	if err != nil {
		return err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return err
	}
	in := map[string]any{}
	if method != "" {
		in["merge_method"] = method
	}
	return githubCall(ctx, http.MethodPut, url+"/merge", "Bearer "+token, in, nil)
}
