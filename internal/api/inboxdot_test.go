package api_test

// inboxdot_test.go covers spec 056 §4's inbox indicator: renderWeb computes
// HasInboxItems once per request and every page's top bar reads the result.
// The other half — the icon markup and the dot's context plumbing — is
// covered in internal/ui/layout_test.go; these tests drive the real HTTP
// surface so the wiring between webform.go and the store is proven too.

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestInboxDotOnProjectsPage pins the indicator on a page that knows nothing
// about inboxes: /projects shows the dot for a signed-in actor with one
// review awaiting them directly (spec 056 §3.2 bucket 1), and loses it once
// that review is decided.
func TestInboxDotOnProjectsPage(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})

	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user"},
	}
	session := webLogin(t, h, "grace")

	// No pull_requests row is needed: HasInboxItems' bucket 1 and
	// DecideApproval's self-approval check both degrade gracefully when the
	// PR row behind entity_id is absent (approvals.entity_id is free text,
	// no FK to pull_requests).
	actor := "grace"
	seedEvent(t, st, "inbox-dot-approval", func(tx *sql.Tx, _ int64) error {
		return store.InsertAwaitingApproval(tx, st.Now(), "pr",
			store.PREntityID("acme/widgets", 3), "shainboxdot", nil, &actor)
	})

	rows, err := st.ListAwaitingApprovals(context.Background())
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	var approvalID int64
	for _, row := range rows {
		if row.EntityID == store.PREntityID("acme/widgets", 3) {
			approvalID = row.ID
		}
	}
	if approvalID == 0 {
		t.Fatalf("seeded approval not found in the awaiting queue")
	}

	rr := withSession(t, h, "GET", "/projects", session, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("/projects status = %d, body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, `class="dot dot-alert"`) {
		t.Errorf("/projects: no inbox dot for an actor with one review awaiting\n%s", body)
	}

	decidePath := "/approvals/" + strconv.FormatInt(approvalID, 10) + "/decide"
	decideRR := withSession(t, h, "POST", decidePath, session, "decision=approve")
	if decideRR.Code != http.StatusSeeOther {
		t.Fatalf("decide approval status = %d, body %s", decideRR.Code, decideRR.Body.String())
	}

	rr2 := withSession(t, h, "GET", "/projects", session, "")
	if body := rr2.Body.String(); strings.Contains(body, "dot-alert") {
		t.Errorf("/projects: inbox dot still shown after the review was decided\n%s", body)
	}
}

// TestInboxDotAbsentSignedOut covers the open-deployment path (newTestServer,
// WebOpen): the anonymous subject names no actor, so renderWeb never has an
// actor id to ask HasInboxItems about, and the icon renders without a dot.
func TestInboxDotAbsentSignedOut(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)

	body := doReq(t, h, "GET", "/projects", "", nil).Body.String()
	if !strings.Contains(body, `aria-label="Inbox"`) {
		t.Errorf("/projects: missing the inbox icon\n%s", body)
	}
	if strings.Contains(body, "dot-alert") {
		t.Errorf("/projects: signed-out request rendered an inbox dot\n%s", body)
	}
}

// TestHasInboxItemsCalledOnce is a structural guard for spec 056 §4's
// once-per-request rule: renderWeb (webform.go) is the only place in
// internal/api that calls store.Store.HasInboxItems. A second call site
// would mean either a duplicate query per page or a page computing the flag
// itself instead of reading it off the context renderWeb already set, both
// of which this test catches without needing to instrument the store.
func TestHasInboxItemsCalledOnce(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no source files found; the glob is wrong, not the package")
	}
	fset := token.NewFileSet()
	calls := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "HasInboxItems" {
				calls++
				if filepath.Base(path) != "webform.go" {
					t.Errorf("%s:%d calls HasInboxItems; renderWeb (webform.go) must be the only call site (056 §4)",
						filepath.Base(path), fset.Position(call.Pos()).Line)
				}
			}
			return true
		})
	}
	if calls != 1 {
		t.Errorf("HasInboxItems called from %d call sites in internal/api, want 1 (renderWeb)", calls)
	}
}
