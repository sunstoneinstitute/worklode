//go:build e2e

// doc_patch_test.go drives 025 §7.3's reviewer gate and §8.4's in-place
// amendment path end to end over public surfaces only: the two lanes a
// document's reviewer set opens, the accept those lanes refuse until both
// approve, a non-substantive patch and the note that records it, a patch a
// referring spec refuses, and a substantive patch that reopens the lanes,
// marks the sections it touched, and mints the re-review task.
//
// It is the first e2e test that decides an approval rather than only proving
// the refusal (approvals_test.go and approval_flows_test.go both stop at the
// 403), so the stack runs behind a real fake-Keycloak issuer instead of
// LODE_WEB_OPEN, and each reviewer logs in over the wire before deciding
// their own lane. Everything else goes through the API client.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

// The four bodies of the spec under amendment, in the order the test lands
// them. Each patch touches prose inside one existing section and nothing
// else: an added or removed anchor is refused by the §6 anchor freeze, and a
// changed wl:/wlc: term, code span or acceptance heading by §8.3's mechanical
// rules, neither of which is what this test is about.
//
// The frontmatter status stays "draft" throughout on purpose. It is the
// creation-time signal only; what makes the document accepted is the docs
// column AcceptDoc moves, and a patch never reads it back.
const gateSpecV1 = `---
status: draft
---

# Gate Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// gateSpecV2 is the non-substantive fix: sec-2's prose, nothing else.
const gateSpecV2 = `---
status: draft
---

# Gate Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body, with the stray word removed.
`

// gateSpecV2b touches sec-2 once more — the edit the requiring spec refuses.
const gateSpecV2b = `---
status: draft
---

# Gate Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body, rewritten again.
`

// gateSpecV3 is the substantive amendment, on the section nothing refers to.
const gateSpecV3 = `---
status: draft
---

# Gate Spec

Intro.

## 1. Scope {#sec-1}

Scope body, widened to cover the adjacent case.

## 2. Model {#sec-2}

Model body, with the stray word removed.
`

// requiringSpecBody is the second accepted spec: it declares a dependence on
// the gate spec's sec-2, which is what makes that section unpatchable (§8.2).
const requiringSpecBody = `---
status: draft
requires: gate-spec#sec-2
---

# Requiring Spec

Intro.

## 1. Dependence {#sec-1}

Built on the gate spec's model.
`

// cookieNamed reads one Set-Cookie value off a response, failing the test
// when the response set none.
func cookieNamed(t *testing.T, resp *http.Response, name, who string) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("%s: response set no %s cookie (status %d)", who, name, resp.StatusCode)
	return ""
}

// webSession logs username in through the whole Keycloak round trip on the
// real wire — /auth/login, the signed state cookie, /auth/callback — and
// returns their wl_session cookie, provisioning the human actor as a side
// effect. claims are the ID token's, so the caller states exactly which
// identity and groups the session carries.
//
// internal/api's own webLogin drives the handler in process; this one cannot,
// and it carries both cookies by hand rather than through a cookiejar: they
// are set Secure, and a jar declines to send those back over the plain HTTP
// an httptest server speaks.
func webSession(t *testing.T, base string, iss *oidctest.Issuer, username string) string {
	t.Helper()
	iss.TokenClaims = map[string]any{
		"preferred_username": username,
		"name":               username,
		"aud":                iss.ClientID,
		"groups":             []string{"user"},
	}
	login, err := noRedirect.Get(base + "/auth/login?next=/")
	if err != nil {
		t.Fatalf("GET /auth/login as %s: %v", username, err)
	}
	io.Copy(io.Discard, login.Body)
	login.Body.Close()
	if login.StatusCode != http.StatusFound {
		t.Fatalf("GET /auth/login as %s: status = %d, want 302", username, login.StatusCode)
	}
	oauth := cookieNamed(t, login, "wl_oauth", username)
	authorize, err := url.Parse(login.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the authorize redirect for %s: %v", username, err)
	}
	state := authorize.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize redirect for %s carries no state: %s", username, authorize)
	}

	req, err := http.NewRequest(http.MethodGet,
		base+"/auth/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	if err != nil {
		t.Fatalf("build the callback request for %s: %v", username, err)
	}
	req.AddCookie(&http.Cookie{Name: "wl_oauth", Value: oauth})
	cb, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/callback as %s: %v", username, err)
	}
	body, _ := io.ReadAll(cb.Body)
	cb.Body.Close()
	if cb.StatusCode != http.StatusFound {
		t.Fatalf("GET /auth/callback as %s: status = %d, want 302; body %s",
			username, cb.StatusCode, body)
	}
	return cookieNamed(t, cb, "wl_session", username)
}

// decideAs submits the cockpit's decide form as a logged-in browser would —
// session cookie, same-origin headers — and returns the status and body.
func decideAs(t *testing.T, base, session string, approvalID int64, decision string) (int, string) {
	t.Helper()
	form := url.Values{"decision": {decision}}
	target := fmt.Sprintf("%s/approvals/%d/decide", base, approvalID)
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build the decide request for approval %d: %v", approvalID, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})

	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// getPageAs fetches a session-gated cockpit page. With an issuer configured
// every page is behind webGuard, so getPage's anonymous fetch only ever sees
// the login redirect.
func getPageAs(t *testing.T, target, session string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", target, err)
	}
	req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return resp.StatusCode, string(body)
}

// docLanes reads the awaiting queue and keeps the rows governing one
// document, oldest first. The queue takes no entity_kind filter, so the
// selection happens here; the read itself is GET /api/v1/approvals and
// nothing else.
func docLanes(t *testing.T, ctx context.Context, c *cli.Client, docID int64) []model.AwaitingApproval {
	t.Helper()
	resp, _, err := c.ListApprovals(ctx)
	if err != nil {
		t.Fatalf("GET /api/v1/approvals: %v", err)
	}
	want := model.DocEntityID(docID)
	var out []model.AwaitingApproval
	for _, a := range resp.Approvals {
		if a.EntityKind == "doc" && a.EntityID == want {
			out = append(out, a)
		}
	}
	return out
}

// describeLanes renders the awaiting lanes for a failure message.
func describeLanes(lanes []model.AwaitingApproval) string {
	if len(lanes) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(lanes))
	for _, a := range lanes {
		parts = append(parts, fmt.Sprintf("#%d(lane=%s rev=%s state=%s)",
			a.ID, a.Lane, a.SubjectRevision, a.State))
	}
	return strings.Join(parts, ", ")
}

// wantLanes asserts the document's open lanes are exactly the named
// reviewers at version, and returns them keyed by reviewer.
func wantLanes(t *testing.T, ctx context.Context, c *cli.Client,
	docID int64, version int, reviewers []string, why string) map[string]model.AwaitingApproval {
	t.Helper()
	lanes := docLanes(t, ctx, c, docID)
	byReviewer := make(map[string]model.AwaitingApproval, len(lanes))
	revision := strconv.Itoa(version)
	for _, a := range lanes {
		if a.SubjectRevision != revision {
			t.Fatalf("%s: lane %s is at revision %s, want %s (%s)",
				why, a.Lane, a.SubjectRevision, revision, describeLanes(lanes))
		}
		byReviewer[a.Lane] = a
	}
	if len(byReviewer) != len(reviewers) {
		t.Fatalf("%s: %d awaiting lanes on doc %d, want %d: %s",
			why, len(byReviewer), docID, len(reviewers), describeLanes(lanes))
	}
	for _, r := range reviewers {
		if _, ok := byReviewer[r]; !ok {
			t.Fatalf("%s: no awaiting lane for %s: %s", why, r, describeLanes(lanes))
		}
	}
	return byReviewer
}

// sectionByAnchor returns one section of a document detail.
func sectionByAnchor(t *testing.T, d model.DocDetail, anchor string) model.DocSection {
	t.Helper()
	for _, s := range d.Sections {
		if s.Anchor == anchor {
			return s
		}
	}
	t.Fatalf("doc %d has no section #%s: %+v", d.ID, anchor, d.Sections)
	return model.DocSection{}
}

// clientErr asserts err is a 4xx from the API whose message contains each of
// want, and returns it. A refusal that stops naming what it refused is as
// much a regression as one that stops refusing.
func clientErr(t *testing.T, err error, why string, want ...string) *cli.ClientError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: succeeded, want a refusal", why)
	}
	var ce *cli.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("%s: error = %v, want a *cli.ClientError", why, err)
	}
	if ce.Status < 400 || ce.Status >= 500 {
		t.Fatalf("%s: status = %d, want 4xx (%s)", why, ce.Status, ce.Msg)
	}
	for _, w := range want {
		if !strings.Contains(ce.Msg, w) {
			t.Fatalf("%s: message %q does not name %q", why, ce.Msg, w)
		}
	}
	return ce
}

// TestDocPatch walks one accepted spec from its reviewer gate through three
// in-place amendments.
func TestDocPatch(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)

	// The doc-lifecycle loop runs until this context is cancelled, and holds
	// a pooled connection for its advisory lock: it has to stop before the
	// database goes away. Cleanups run LIFO and OpenTestStore registered the
	// drop first, so the cancel below runs ahead of it.
	loopCtx, cancelLoop := context.WithCancel(context.Background())
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		BackgroundCtx:  loopCtx,
		EventPoll:      50 * time.Millisecond,
		// A real issuer rather than WebOpen: deciding an approval is a
		// session act (029 §7.3), and an open instance has no session to
		// decide with.
		OIDCIssuer:    iss.URL(),
		OIDCClientID:  iss.ClientID,
		PublicURL:     "http://localhost:8080",
		SessionSecret: "e2e-session-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		cancelLoop()
		srv.Close()
	})

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "docpatch", Name: "Doc Patch", Key: "DP",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// The two reviewers exist because they logged in: provisioning is what
	// the login does, and SetDocReviewers refuses a name no actor holds. The
	// author is a separate actor, which is what keeps every decision below
	// out of 029 §7.1's self-approval refusal.
	reviewers := []string{"rev-one", "rev-two"}
	sessions := map[string]string{}
	for _, r := range reviewers {
		sessions[r] = webSession(t, srv.URL, iss, r)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "author", Kind: "human", DisplayName: "Author",
	}); err != nil {
		t.Fatalf("create author actor: %v", err)
	}
	authorTok, _, err := admin.CreateToken(ctx, "author", "e2e doc patch", nil)
	if err != nil {
		t.Fatalf("create token for author: %v", err)
	}
	author := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: authorTok.Token})

	// 1. A draft spec with both reviewers assigned. Submitting it is the
	// observation that it is ready to be read (025 §7); opening the lanes is
	// the separate request-approval act, since the reviewer set is durable
	// and the lanes are per version.
	doc, _, err := author.CreateDoc(ctx, model.CreateDocInput{
		Project: "docpatch", Kind: "spec", Number: 1, Slug: "gate-spec",
		Body: gateSpecV1, Owner: "author",
	})
	if err != nil {
		t.Fatalf("create the gate spec: %v", err)
	}
	if doc.Status != "draft" || doc.Version != 1 {
		t.Fatalf("created doc = status %q version %d, want draft version 1", doc.Status, doc.Version)
	}
	if _, _, err := author.SetDocReviewers(ctx, doc.ID, reviewers); err != nil {
		t.Fatalf("set reviewers: %v", err)
	}
	if _, _, err := author.SubmitDoc(ctx, doc.ID); err != nil {
		t.Fatalf("submit doc: %v", err)
	}
	if _, _, err := author.RequestDocApproval(ctx, doc.ID); err != nil {
		t.Fatalf("request approval: %v", err)
	}
	lanes := wantLanes(t, ctx, admin, doc.ID, 1, reviewers, "after requesting approval")

	// 2. The gate: accepting a document its reviewers still owe a decision on
	// is refused, naming both of them. Each reviewer then decides their own
	// lane through the cockpit's one decision route, and the accept lands.
	clientErr(t, func() error { _, _, err := author.AcceptDoc(ctx, doc.ID); return err }(),
		"accept before either reviewer approved", reviewers[0], reviewers[1])

	for _, r := range reviewers {
		code, body := decideAs(t, srv.URL, sessions[r], lanes[r].ID, "approve")
		if code != http.StatusSeeOther {
			t.Fatalf("%s approving lane %d: status = %d, want 303; body %s",
				r, lanes[r].ID, code, body)
		}
	}
	if got := docLanes(t, ctx, admin, doc.ID); len(got) != 0 {
		t.Fatalf("awaiting lanes after both approvals = %s, want none", describeLanes(got))
	}
	accepted, _, err := author.AcceptDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("accept the spec once both reviewers approved: %v", err)
	}
	if accepted.Doc.Status != "accepted" || accepted.Doc.Version != 1 {
		t.Fatalf("accepted doc = status %q version %d, want accepted version 1",
			accepted.Doc.Status, accepted.Doc.Version)
	}

	// The review task the submit minted has served its purpose. Closing it
	// matters to step 5: an open review task about this document is what the
	// review-on-patch rule suppresses itself on.
	submitReview := pollTasksAboutDoc(t, ctx, admin, author, doc.ID, "review", 1,
		"after submitting the spec")[0]
	if _, _, err := author.AbandonTask(ctx, submitReview.ID); err != nil {
		t.Fatalf("abandon the review task %s: %v", submitReview.ID, err)
	}

	// 3. A non-substantive amendment: the version moves, the reviewers are
	// not reopened, and §8.5's note records what changed and why — readable
	// through the API and rendered on the document's own page.
	const note = "Dropped a stray word from the model section."
	patched, _, err := author.PatchDoc(ctx, doc.ID, model.PatchDocInput{
		Body: gateSpecV2, Note: note,
	})
	if err != nil {
		t.Fatalf("non-substantive patch: %v", err)
	}
	if patched.Patch.Classification != "non-substantive" || patched.Patch.RuleFired != "none" {
		t.Fatalf("patch result = %+v, want a non-substantive patch no rule fired on", patched.Patch)
	}
	if patched.Patch.NewVersion != 2 || patched.Doc.Version != 2 {
		t.Fatalf("patch left version %d (doc says %d), want 2",
			patched.Patch.NewVersion, patched.Doc.Version)
	}
	if !slices.Equal(patched.Patch.ChangedAnchors, []string{"sec-2"}) {
		t.Fatalf("changed anchors = %v, want [sec-2]", patched.Patch.ChangedAnchors)
	}
	if patched.Doc.Status != "accepted" {
		t.Fatalf("doc status after the patch = %q, want accepted (§7.3)", patched.Doc.Status)
	}
	if got := docLanes(t, ctx, admin, doc.ID); len(got) != 0 {
		t.Fatalf("a non-substantive patch reopened lanes: %s", describeLanes(got))
	}

	notes, _, err := author.ListDocNotes(ctx, doc.ID)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("doc %d has %d notes, want 1: %+v", doc.ID, len(notes), notes)
	}
	if notes[0].Anchor != "sec-2" || !strings.Contains(notes[0].Body, note) {
		t.Fatalf("note = %+v, want the patch's own note anchored at sec-2", notes[0])
	}
	// A numbered document is served under its citable ref, not its row id:
	// /docs/<id> is the fallback for a document that has no number.
	if doc.Ref == "" {
		t.Fatalf("created doc carries no ref: %+v", doc)
	}
	code, page := getPageAs(t, srv.URL+"/docs/"+doc.Ref, sessions[reviewers[0]])
	if code != http.StatusOK {
		t.Fatalf("GET /docs/%s: status = %d, want 200", doc.Ref, code)
	}
	if !strings.Contains(page, note) {
		t.Fatalf("the document page does not render the patch note:\n%s", page)
	}

	// 4. §8.2: a second accepted spec that requires sec-2 is open work
	// pointing at that text, so amending it again is refused — naming the
	// rule and the section, and pointing at the revision path instead.
	dependent, _, err := author.CreateDoc(ctx, model.CreateDocInput{
		Project: "docpatch", Kind: "spec", Number: 2, Slug: "requiring-spec",
		Body: requiringSpecBody, Owner: "author",
	})
	if err != nil {
		t.Fatalf("create the requiring spec: %v", err)
	}
	if _, _, err := author.AcceptDoc(ctx, dependent.ID); err != nil {
		t.Fatalf("accept the requiring spec: %v", err)
	}
	clientErr(t, func() error {
		_, _, err := author.PatchDoc(ctx, doc.ID, model.PatchDocInput{
			Body: gateSpecV2b, Note: "Another pass at the model.",
		})
		return err
	}(), "patch a section a second spec requires", "referrer", "sec-2", "requiring-spec")

	// 5. A substantive amendment on the section nothing points at: the
	// document stays accepted, the section carries §7.3's patched mark, the
	// reviewers are reopened at the new version, and the watcher mints the
	// re-review task that asks for those decisions.
	substantive, _, err := author.PatchDoc(ctx, doc.ID, model.PatchDocInput{
		Body: gateSpecV3, Substantive: true,
	})
	if err != nil {
		t.Fatalf("substantive patch: %v", err)
	}
	if substantive.Patch.Classification != "substantive" || substantive.Patch.RuleFired != "judged" {
		t.Fatalf("patch result = %+v, want a substantive patch classified by the caller",
			substantive.Patch)
	}
	if substantive.Patch.NewVersion != 3 {
		t.Fatalf("substantive patch left version %d, want 3", substantive.Patch.NewVersion)
	}
	if !slices.Equal(substantive.Patch.ChangedAnchors, []string{"sec-1"}) {
		t.Fatalf("changed anchors = %v, want [sec-1]", substantive.Patch.ChangedAnchors)
	}
	if substantive.Doc.Status != "accepted" {
		t.Fatalf("doc status after the substantive patch = %q, want accepted (§7.3)",
			substantive.Doc.Status)
	}

	detail, _, err := author.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc detail: %v", err)
	}
	if s := sectionByAnchor(t, detail, "sec-1"); !s.Patched || s.LastRevisedIn != 3 {
		t.Fatalf("sec-1 = %+v, want patched and last revised in version 3", s)
	}
	if s := sectionByAnchor(t, detail, "sec-2"); s.Patched {
		t.Fatalf("sec-2 = %+v, want unpatched — this amendment did not touch it", s)
	}
	reopened := wantLanes(t, ctx, admin, doc.ID, 3, reviewers, "after the substantive patch")

	// Two review tasks reference the document now: the abandoned one from the
	// submit, and the mint this patch caused. The count is what the poll
	// waits on — the abandoned row is there from the start, so waiting for
	// one would return before the watcher had run at all.
	reviews := pollTasksAboutDoc(t, ctx, admin, author, doc.ID, "review", 2,
		"after the substantive patch")
	var rereview model.Task
	for _, r := range reviews {
		if r.ID != submitReview.ID {
			rereview = r
		}
	}
	if rereview.ID == "" {
		t.Fatalf("no review task about doc %d beyond the abandoned %s: %s",
			doc.ID, submitReview.ID, describeTasks(reviews))
	}
	if rereview.State != "ready" || rereview.CreatedBy != "watcher" {
		t.Fatalf("re-review task %s = state %q created_by %q, want a ready watcher mint",
			rereview.ID, rereview.State, rereview.CreatedBy)
	}
	if !strings.HasPrefix(rereview.Title, "Re-review patched sections of") ||
		!strings.Contains(rereview.Title, "sec-1") {
		t.Fatalf("re-review task title = %q, want it to name the patched section", rereview.Title)
	}

	// The mark comes off when the last reopened lane is approved, and not
	// before: after the first decision the section is still patched.
	code, body := decideAs(t, srv.URL, sessions[reviewers[0]], reopened[reviewers[0]].ID, "approve")
	if code != http.StatusSeeOther {
		t.Fatalf("%s approving the reopened lane: status = %d, want 303; body %s",
			reviewers[0], code, body)
	}
	detail, _, err = author.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc detail after one re-approval: %v", err)
	}
	if s := sectionByAnchor(t, detail, "sec-1"); !s.Patched {
		t.Fatalf("sec-1 = %+v, want still patched while %s owes a decision", s, reviewers[1])
	}

	code, body = decideAs(t, srv.URL, sessions[reviewers[1]], reopened[reviewers[1]].ID, "approve")
	if code != http.StatusSeeOther {
		t.Fatalf("%s approving the reopened lane: status = %d, want 303; body %s",
			reviewers[1], code, body)
	}
	detail, _, err = author.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc detail after both re-approvals: %v", err)
	}
	if s := sectionByAnchor(t, detail, "sec-1"); s.Patched {
		t.Fatalf("sec-1 = %+v, want the patched mark cleared once every reopened lane approved", s)
	}
	if got := docLanes(t, ctx, admin, doc.ID); len(got) != 0 {
		t.Fatalf("awaiting lanes after both re-approvals = %s, want none", describeLanes(got))
	}

	// 6. The log says what happened, in the terms §8.4 classifies an
	// amendment by: two doc.patched events, one of each classification, each
	// carrying the rule that decided it. The refused patch in step 4 wrote
	// none — its transaction rolled back with the refusal.
	events := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: watcher.TypeDocPatched}, 2)
	if len(events) != 2 {
		t.Fatalf("%s events = %d, want 2 (the refused patch logs nothing)",
			watcher.TypeDocPatched, len(events))
	}
	rules := map[string]string{}
	for _, ev := range events {
		payload := eventPayload(t, ev)
		class, _ := payload["classification"].(string)
		rule, _ := payload["rule"].(string)
		if class == "" || rule == "" {
			t.Fatalf("%s event %d payload = %v, want a classification and a rule",
				watcher.TypeDocPatched, ev.ID, payload)
		}
		rules[class] = rule
	}
	if rules["non-substantive"] != "none" || rules["substantive"] != "judged" {
		t.Fatalf("doc.patched classifications = %v, want non-substantive/none and substantive/judged", rules)
	}
}
