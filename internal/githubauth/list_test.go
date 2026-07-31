package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// listRecorder captures the query string of every request the pager sends to
// the list endpoint, in order, so a test can assert on parameters (since,
// state) beyond what the served pages already exercise.
type listRecorder struct {
	mu      sync.Mutex
	queries []url.Values
}

func (r *listRecorder) record(q url.Values) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, q)
}

func (r *listRecorder) last() url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queries) == 0 {
		return nil
	}
	return r.queries[len(r.queries)-1]
}

// listServer serves /repos/acme/widgets/{issues,pulls} from fixed pages, plus
// the two calls InstallationToken makes. pages[i] is page i+1. Every request
// to the list endpoint must carry per_page=maxPerPage — the pager's
// short-page-means-last-page check only holds if it requested exactly that
// many — and is recorded in the returned *listRecorder for callers that want
// to assert on other query parameters (e.g. since).
func listServer(t *testing.T, kind string, pages [][]map[string]any) (*AppAuth, *listRecorder) {
	t.Helper()
	rec := &listRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/" + kind:
			rec.record(r.URL.Query())
			if got := r.URL.Query().Get("per_page"); got != strconv.Itoa(maxPerPage) {
				t.Errorf("per_page = %q, want %d — the pager's short-page check assumes this page size", got, maxPerPage)
			}
			page := 1
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			if page < 1 || page > len(pages) {
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			json.NewEncoder(w).Encode(pages[page-1])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &AppAuth{AppID: "12345", Key: testKey(), BaseURL: srv.URL}, rec
}

// fullIssuePage builds a page of exactly maxPerPage issue entries so the
// pager keeps going.
func fullIssuePage(start int) []map[string]any {
	page := make([]map[string]any, 0, maxPerPage)
	for i := 0; i < maxPerPage; i++ {
		page = append(page, map[string]any{
			"number": start + i, "title": "t", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
		})
	}
	return page
}

// fullPullPage builds a page of exactly maxPerPage pull-request entries so
// the pager keeps going. Unlike fullIssuePage it carries a head key, since a
// pull request always has one.
func fullPullPage(start int) []map[string]any {
	page := make([]map[string]any, 0, maxPerPage)
	for i := 0; i < maxPerPage; i++ {
		page = append(page, map[string]any{
			"number": start + i, "title": "t", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
			"head": map[string]any{"ref": "r", "sha": "s"},
		})
	}
	return page
}

func TestListIssuesSkipsPullRequests(t *testing.T) {
	app, _ := listServer(t, "issues", [][]map[string]any{{
		{"number": 1, "title": "real issue", "state": "open",
			"html_url": "https://gh/1", "updated_at": "2026-01-01T00:00:00Z"},
		{"number": 2, "title": "a PR", "state": "open",
			"html_url": "https://gh/2", "updated_at": "2026-01-01T00:00:00Z",
			"pull_request": map[string]any{"url": "https://api/pulls/2"}},
	}})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("got %+v, want only issue 1 — entries with a pull_request key must be skipped", got)
	}
}

func TestListIssuesPagesUntilShortPage(t *testing.T) {
	app, _ := listServer(t, "issues", [][]map[string]any{fullIssuePage(1), {
		{"number": 999, "title": "last", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z"},
	}})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != maxPerPage+1 {
		t.Fatalf("len = %d, want %d", len(got), maxPerPage+1)
	}
}

func TestListIssuesTruncatesAtMaxPages(t *testing.T) {
	app, _ := listServer(t, "issues", [][]map[string]any{fullIssuePage(1), fullIssuePage(101)})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 2)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true — two full pages with maxPages=2 means more may remain")
	}
	if len(got) != 2*maxPerPage {
		t.Fatalf("len = %d, want %d", len(got), 2*maxPerPage)
	}
}

// TestListIssuesSendsSince catches a since that is built but never attached
// to the request (or attached under the wrong key): --since is the
// documented recovery path after a truncated import, so a silently dropped
// parameter would mean every retry re-truncates the same way.
func TestListIssuesSendsSince(t *testing.T) {
	app, rec := listServer(t, "issues", [][]map[string]any{{}})
	since := time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)
	_, _, err := app.ListIssues(context.Background(), "acme/widgets", "open", since, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	want := since.Format(time.RFC3339)
	if got := rec.last().Get("since"); got != want {
		t.Fatalf("since = %q, want %q", got, want)
	}
}

// TestListIssuesOmitsSinceWhenZero guards the other direction: a zero Time
// must disable the filter entirely rather than serializing as the zero-value
// timestamp GitHub would reject or misinterpret.
func TestListIssuesOmitsSinceWhenZero(t *testing.T) {
	app, rec := listServer(t, "issues", [][]map[string]any{{}})
	_, _, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if rec.last().Has("since") {
		t.Fatalf("since = %q, want the parameter omitted for a zero Time", rec.last().Get("since"))
	}
}

func TestListPullsDerivesMergedState(t *testing.T) {
	app, _ := listServer(t, "pulls", [][]map[string]any{{
		{"number": 1, "title": "open one", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
			"head": map[string]any{"ref": "lode/WL-1-x", "sha": "abc"}},
		{"number": 2, "title": "merged one", "state": "closed",
			"html_url": "u", "updated_at": "2026-01-02T00:00:00Z",
			"merged_at": "2026-01-02T00:00:00Z", "merge_commit_sha": "def",
			"head": map[string]any{"ref": "lode/WL-2-y", "sha": "bbb"}},
		{"number": 3, "title": "closed unmerged", "state": "closed",
			"html_url": "u", "updated_at": "2026-01-03T00:00:00Z",
			"head": map[string]any{"ref": "x", "sha": "ccc"}},
	}})
	got, _, err := app.ListPulls(context.Background(), "acme/widgets", "all", 20)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// The list endpoint has no "merged" boolean; it must come from merged_at.
	if got[0].Merged || !got[1].Merged || got[2].Merged {
		t.Fatalf("merged flags = %v/%v/%v, want false/true/false",
			got[0].Merged, got[1].Merged, got[2].Merged)
	}
	if got[1].HeadRef != "lode/WL-2-y" || got[1].HeadSHA != "bbb" {
		t.Fatalf("head = %q/%q, want lode/WL-2-y/bbb", got[1].HeadRef, got[1].HeadSHA)
	}
}

// TestListPullsPagesUntilShortPage and TestListPullsTruncatesAtMaxPages
// mirror the ListIssues pagination tests above. ListPulls has its own
// hand-written pager loop (not shared with ListIssues), so passing on the
// issues side proves nothing about it: flipping ListPulls' short-page
// comparison from < to <= breaks nothing else in this suite.
func TestListPullsPagesUntilShortPage(t *testing.T) {
	app, _ := listServer(t, "pulls", [][]map[string]any{fullPullPage(1), {
		{"number": 999, "title": "last", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
			"head": map[string]any{"ref": "r", "sha": "s"}},
	}})
	got, truncated, err := app.ListPulls(context.Background(), "acme/widgets", "all", 20)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != maxPerPage+1 {
		t.Fatalf("len = %d, want %d", len(got), maxPerPage+1)
	}
}

func TestListPullsTruncatesAtMaxPages(t *testing.T) {
	app, _ := listServer(t, "pulls", [][]map[string]any{fullPullPage(1), fullPullPage(101)})
	got, truncated, err := app.ListPulls(context.Background(), "acme/widgets", "all", 2)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true — two full pages with maxPages=2 means more may remain")
	}
	if len(got) != 2*maxPerPage {
		t.Fatalf("len = %d, want %d", len(got), 2*maxPerPage)
	}
}
