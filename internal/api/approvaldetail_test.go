package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// getOK issues a GET and fails the test unless it answers 200, returning the
// body — the approval detail page's tests read the rendered HTML for facts
// (a revision, a state, a link), not a JSON shape.
func getOK(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rr := doReq(t, h, "GET", path, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body %s", path, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// TestApprovalDetailShowsRevisionHistory: the candidate's own detail page
// renders the full decision history (ListApprovalsForEntity, 032 §7), not
// just its own row — a stale approval beside an open candidate has to read
// as exactly that.
func TestApprovalDetailShowsRevisionHistory(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	id := seedDecidedThenCandidate(t, st, "acme/site#7", "aaa111", "bbb222")

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", id))
	for _, want := range []string{"aaa111", "bbb222", "awaiting", "approved"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}

// TestApprovalDetailUnknownID404s: an id nothing names 404s, like every
// other detail page's unknown-id handling (docPage, taskPage).
func TestApprovalDetailUnknownID404s(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "GET", "/approvals/999999999", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestApprovalDetailRendersGovernedReferences: the review-graph list (029
// §7.1, 032 §7) renders every reference this approval's own designation
// recorded, each as "kind id @ revision".
func TestApprovalDetailRendersGovernedReferences(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	id := seedAwaitingApprovalRow(t, st, "pr", "acme/gov#1", "rev1", "")
	seedEvent(t, st, "approval-detail-governed-refs", func(tx *sql.Tx, _ int64) error {
		return store.InsertGovernedRefs(tx, st.Now(), nil, "pr", "acme/gov#1", "rev1",
			[]store.GovernedRef{{Kind: "doc", ID: "doc:1", Revision: "3"}})
	})

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", id))
	if !strings.Contains(body, "doc doc:1 @ 3") {
		t.Errorf("detail page missing governed reference %q:\n%s", "doc doc:1 @ 3", body)
	}
}

// TestApprovalDetailGovernedReferencesEmptyIsHonest: an approval whose
// designation recorded no references says so plainly rather than fabricating
// a lineage.
func TestApprovalDetailGovernedReferencesEmptyIsHonest(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/site#8", "No governed refs here")

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", seeded.ID))
	if !strings.Contains(body, "None recorded.") {
		t.Errorf("detail page does not state an empty governed-reference list honestly:\n%s", body)
	}
}

// TestApprovalDetailCompareLinkOnlyWithDecidedPredecessor: the GitHub
// diff-from-previous jump-out link (032 §7) appears for a candidate that
// follows a decided review, and not for a first-ever review with no
// predecessor to diff against.
func TestApprovalDetailCompareLinkOnlyWithDecidedPredecessor(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)

	t.Run("with predecessor", func(t *testing.T) {
		id := seedDecidedThenCandidate(t, st, "acme/gizmo#1", "aaa111", "bbb222")
		body := getOK(t, h, fmt.Sprintf("/approvals/%d", id))
		if !strings.Contains(body, "/compare/aaa111...bbb222") {
			t.Errorf("detail page missing compare link:\n%s", body)
		}
	})

	t.Run("no predecessor", func(t *testing.T) {
		seeded := seedAwaitingPRApproval(t, st, "acme/gizmo#2", "First review, nothing to diff")
		body := getOK(t, h, fmt.Sprintf("/approvals/%d", seeded.ID))
		if strings.Contains(body, "/compare/") {
			t.Errorf("detail page offers a compare link with no decided predecessor:\n%s", body)
		}
	})
}

// TestApprovalDetailHasOneAriaCurrent: the page marks no global-nav
// destination current (PageProps.ActiveGlobal is empty, like Reviews and the
// task page), but the decision-history row matching the page's own id links
// to itself and carries aria-current="page" — the ordinary use of the
// attribute for the current item in a set of pages. Exactly one, never zero
// or two.
func TestApprovalDetailHasOneAriaCurrent(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	id := seedDecidedThenCandidate(t, st, "acme/aria#1", "aaa111", "bbb222")

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", id))
	if got := strings.Count(body, `aria-current="page"`); got != 1 {
		t.Errorf(`aria-current="page" count = %d, want 1:%s`, got, body)
	}
	if !strings.Contains(body, `href="/approvals/`+strconv.FormatInt(id, 10)+`" aria-current="page"`) {
		t.Errorf("aria-current=\"page\" is not on the history row for this page's own id:\n%s", body)
	}
}
