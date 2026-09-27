package api_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestTaskPageShowsOpenPRs asserts the task page header links a task's open
// pull requests (WL-933): each as "#<number>" to its GitHub URL, followed by
// a CI-state dot rolled up from its head SHA's runs. A merged PR stays out
// of the header — it is already covered by TestTaskPage's timeline
// assertions — so the header region (between the title and the "Created"
// line) is checked in isolation rather than the whole page body, since the
// timeline itself also links every PR's URL.
func TestTaskPageShowsOpenPRs(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Add feature", "body": "do the thing", "priority": "high", "kind": "feature",
	})

	const repo = "org/app"
	seedEvent(t, st, "pr-open", func(tx *sql.Tx, _ int64) error {
		_, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "open",
			HeadRef: "WL-1-add-feature", HeadSHA: "headsha1",
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(),
		}, "")
		return err
	})
	seedEvent(t, st, "ci-run", func(tx *sql.Tx, _ int64) error {
		return store.UpsertCIRun(tx, store.CIRun{
			Repo: repo, HeadSHA: "headsha1", Workflow: "ci", Status: "completed",
			Conclusion: strPtr("success"), StartedAt: st.Now(),
		})
	})

	const mergeSHA = "mergesha2"
	seedEvent(t, st, "pr-merge", func(tx *sql.Tx, _ int64) error {
		merged := st.Now()
		ms := mergeSHA
		_, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 8, Title: "Add feature part 2", State: "merged",
			HeadRef: "WL-1-second", HeadSHA: "headsha2", MergeSHA: &ms,
			URL: "https://github.com/org/app/pull/8", OpenedAt: st.Now(), MergedAt: &merged,
		}, "")
		return err
	})

	rr := getCanonical(t, h, "/tasks/WL-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	start := strings.Index(body, "<h1>")
	end := strings.Index(body, `<p class="muted small">Created`)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("could not locate task page header region:\n%s", body)
	}
	header := body[start:end]
	bodyContains(t, header, `<a href="https://github.com/org/app/pull/7">#7</a>`, `class="dot ci-passed"`)
	if strings.Contains(header, "pull/8") {
		t.Fatalf("merged PR unexpectedly linked in task page header:\n%s", header)
	}
}
