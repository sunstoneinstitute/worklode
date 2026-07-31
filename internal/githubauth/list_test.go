package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// listServer serves /repos/acme/widgets/{issues,pulls} from fixed pages, plus
// the two calls InstallationToken makes. items[i] is page i+1.
func listServer(t *testing.T, kind string, pages [][]map[string]any) *AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/" + kind:
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
	return &AppAuth{AppID: "12345", Key: testKey(), BaseURL: srv.URL}
}

// full builds a page of exactly maxPerPage entries so the pager keeps going.
func full(start int) []map[string]any {
	page := make([]map[string]any, 0, maxPerPage)
	for i := 0; i < maxPerPage; i++ {
		page = append(page, map[string]any{
			"number": start + i, "title": "t", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
		})
	}
	return page
}

func TestListIssuesSkipsPullRequests(t *testing.T) {
	app := listServer(t, "issues", [][]map[string]any{{
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
	app := listServer(t, "issues", [][]map[string]any{full(1), {
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
	app := listServer(t, "issues", [][]map[string]any{full(1), full(101)})
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

func TestListPullsDerivesMergedState(t *testing.T) {
	app := listServer(t, "pulls", [][]map[string]any{{
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
