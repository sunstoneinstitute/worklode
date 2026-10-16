// webform.go makes the cockpit writable: the two creation forms (a new task,
// a declared deliverable) and the POST handlers behind them. Everything else
// in the web UI is a projection; these are the only routes that write.
//
// Three properties hold for every handler here:
//
//   - They write through the same code the JSON API writes through
//     (store.CreateTask via RecordEvent, recordDeliverable), so a task typed
//     into a browser and one posted by the CLI are indistinguishable
//     afterwards except for the event source, which is "web" precisely so
//     that "who typed this" stays answerable.
//   - They are POST-redirect-GET. A rejected submit re-renders the form with
//     the message and everything the person typed; a successful one 303s to
//     the created object, so a reload never creates a second one.
//   - They accept only same-origin submissions (see sameOriginForm). The
//     session cookie is already SameSite=Lax, which keeps a cross-site POST
//     from carrying it; the header check is the second lock, and the one that
//     still holds in a deployment with no login provider configured, where the
//     subject is the anonymous authOpen one and there is no cookie to withhold.
//
// The creation forms carry permWebWrite and the decide route carries
// permApprovalDecide (routeGuards), so reaching any of them is a policy
// decision made in authz.go, not something these handlers re-check. The
// decide route carries one gate more — requireSession, applied at
// registration — because WL-REQ-124 makes deciding an approval a web-session
// act rather than something a long-lived bearer token can do.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// maxWebForm caps a web form submission at 256 KiB — generous for a task body
// typed by a person, small enough that an unauthenticated open deployment
// cannot be made to buffer megabytes per request.
const maxWebForm = 256 << 10

// deliverableFormValues are the deliverable form's fields as submitted.
type deliverableFormValues struct {
	Name        string
	Description string
	URL         string
	Artifact    string
	Label       bool
	Milestone   string
}

// sameOriginForm reports whether a state-changing form submission came from
// this application's own pages. Sec-Fetch-Site is authoritative where the
// browser sends it ("none" is a direct navigation, which a form POST is not,
// but curl and the e2e harness send neither header and must still work);
// otherwise an Origin, if present, must match the request host or
// LODE_PUBLIC_URL's.
func (s *server) sameOriginForm(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	return s.originAllowed(r)
}

// originAllowed is the Sec-Fetch-Site-less half of the origin check, shared
// by the form guard and the JSON one: an Origin, if present, must match the
// request host or LODE_PUBLIC_URL's (WL-REQ-1308). No Origin at
// all passes — curl and the e2e harness send neither header.
func (s *server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if s.cfg.PublicURL != "" {
		if pub, err := url.Parse(s.cfg.PublicURL); err == nil && pub.Host != "" && strings.EqualFold(u.Host, pub.Host) {
			return true
		}
	}
	return false
}

// projectHeader loads the project identity the project-scoped shell needs
// (name and key beside the local navigation). ErrNotFound propagates, so an
// unknown project 404s the same way every other project route does.
func (s *server) projectHeader(ctx context.Context, id string) (ui.CockpitProject, error) {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		return ui.CockpitProject{}, err
	}
	return ui.CockpitProject{
		ID: p.ID, Name: p.Name, Key: p.Key, HasSpecs: s.hasSpecs(ctx, p.ID),
	}, nil
}

// parseWebForm caps and parses a form body. A body over the cap or a
// malformed encoding is a 400 — there is no form to re-render values into
// when the values could not be read.
func parseWebForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebForm)
	if err := r.ParseForm(); err != nil {
		webErr(w, http.StatusBadRequest, "could not read the submitted form")
		return false
	}
	return true
}

// beginFormPost runs the three gates every creation-form POST shares —
// same origin, a resolvable project, a readable body — counting the refusal
// under the named form. A false ok means the response is already written.
func (s *server) beginFormPost(w http.ResponseWriter, r *http.Request, form string) (ui.CockpitProject, bool) {
	if !s.sameOriginForm(r) {
		s.observeFormSubmission(form, "forbidden")
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return ui.CockpitProject{}, false
	}
	project, err := s.projectHeader(r.Context(), r.PathValue("id"))
	if err != nil {
		s.observeFormSubmission(form, formOutcome(err))
		s.webStoreErr(w, err)
		return ui.CockpitProject{}, false
	}
	if !parseWebForm(w, r) {
		s.observeFormSubmission(form, "invalid")
		return ui.CockpitProject{}, false
	}
	return project, true
}

// renderWeb writes one rendered page with the given status. Form pages use it
// for the 422 re-render; the plain GET pages go through it too, so the
// content type and the Content-Security-Policy are set in one place — every
// templ page renders through here, and a task body is untrusted markup
// (WL-REQ-231), so a page that skipped the policy would be the one that
// needs it.
//
// It is also where the admin flag every page reads via ui.WithAdmin/isAdmin
// is set, from the same Subject the route guards used — one read per request,
// so no page recomputes roles.
//
// It is also the one place WL-REQ-333's inbox indicator is computed: when
// the request names an actor, one HasInboxItems call decides the flag every
// page's top bar reads via ui.WithInboxDot/inboxDot, before rendering. No
// other call site computes it — that is what makes "once per request"
// structural rather than a convention every handler has to remember. A store
// error is logged and falls through to rendering without a dot; the
// indicator must never fail a page.
func (s *server) renderWeb(w http.ResponseWriter, r *http.Request, status int, page string, c templ.Component) {
	ctx := r.Context()
	sub := subjectFrom(r)
	ctx = ui.WithAdmin(ctx, sub.HasRole(RoleAdmin))
	if actorID := sub.ActorID; actorID != "" {
		ctx = ui.WithActor(ctx, actorID, sub.DisplayName)
		has, err := s.st.HasInboxItems(ctx, actorID)
		if err != nil {
			s.log.Error("check inbox items", "err", err)
		} else {
			ctx = ui.WithInboxDot(ctx, has)
		}
	}
	s.setWebHeaders(w)
	w.WriteHeader(status)
	if err := c.Render(ctx, w); err != nil {
		s.log.Error("render "+page, "err", err)
	}
}

// --- deliverables -----------------------------------------------------------

// deliverablesPage handles GET /projects/{id}/deliverables: the project's
// declared deliverables and the affordance to declare another. It replaces
// the honest placeholder this destination used to render.
func (s *server) deliverablesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	items, err := s.st.ListDeliverables(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	milestones, err := s.st.ListMilestones(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "deliverables page", ui.Deliverables(deliverablesView(project, items, milestones)))
}

// newDeliverablePage handles GET /projects/{id}/deliverables/new.
func (s *server) newDeliverablePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	milestones, err := s.st.ListMilestones(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	s.renderWeb(w, r, http.StatusOK, "new deliverable page",
		ui.NewDeliverable(newDeliverableView(project, deliverableFormValues{}, milestones, "", s.dictationEnabled())))
}

// createDeliverableFromForm handles POST /projects/{id}/deliverables: declare
// the deliverable and 303 back to the list, or re-render at 422.
func (s *server) createDeliverableFromForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, ok := s.beginFormPost(w, r, "deliverable")
	if !ok {
		return
	}

	values := deliverableFormValues{
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
		URL:         strings.TrimSpace(r.PostFormValue("url")),
		Artifact:    strings.TrimSpace(r.PostFormValue("artifact")),
		Label:       r.PostFormValue("label") != "",
		Milestone:   strings.TrimSpace(r.PostFormValue("milestone")),
	}
	in, msg := validateDeliverable(project.ID, values.Name, values.Description, values.URL, values.Artifact, values.Label, values.Milestone, actorIDFrom(r))
	if msg != "" {
		s.observeFormSubmission("deliverable", "invalid")
		milestones, err := s.st.ListMilestones(ctx, project.ID)
		if err != nil {
			s.webStoreErr(w, err)
			return
		}
		s.renderWeb(w, r, http.StatusUnprocessableEntity, "new deliverable page",
			ui.NewDeliverable(newDeliverableView(project, values, milestones, formMessage(msg), s.dictationEnabled())))
		return
	}

	if _, err := s.recordDeliverable(ctx, "web", in); err != nil {
		s.observeFormSubmission("deliverable", formOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	s.observeFormSubmission("deliverable", "created")
	http.Redirect(w, r, "/projects/"+project.ID+"/deliverables", http.StatusSeeOther)
}

// reportDeliverableFromForm handles POST /deliverables/{id}/report: the
// Report control on a deliverable row (WL-RULE-121). Deliverable ids are globally
// unique, so the route needs no project segment; the redirect back to the
// project's deliverables page reads the project off the deliverable instead.
//
// It is not beginFormPost's shape — that one resolves a project from the path
// this route does not carry — so the two shared gates are applied here: same
// origin, and a readable form body.
func (s *server) reportDeliverableFromForm(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginForm(r) {
		s.observeDeliverableReport("web", "invalid")
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return
	}
	if !parseWebForm(w, r) {
		s.observeDeliverableReport("web", "invalid")
		return
	}
	id := r.PathValue("id")
	d, err := s.st.GetDeliverable(r.Context(), id)
	if err != nil {
		s.observeDeliverableReport("web", deliverableReportOutcome(err))
		s.webStoreErr(w, err)
		return
	}
	// The menu offers only the five, so anything else is a hand-built request
	// and gets the terse refusal rather than a re-rendered page.
	state := r.PostFormValue("state")
	if ok, msg := validReportState(state); !ok {
		s.observeDeliverableReport("web", "invalid")
		webErr(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if err := s.reportDeliverableState(r.Context(), "web", id, state,
		actorIDFrom(r), strings.TrimSpace(r.PostFormValue("note"))); err != nil {
		s.webStoreErr(w, err)
		return
	}
	http.Redirect(w, r, "/projects/"+d.Project+"/deliverables", http.StatusSeeOther)
}

// formMessage turns a JSON-API validation message ("name is required") into
// the sentence the form shows. The API's wording is terse by convention; a
// person reading a form is owed a sentence.
func formMessage(msg string) string {
	switch msg {
	case "name is required":
		return "A name is required."
	case "url must be an absolute http or https address":
		return "The URL must be an absolute http:// or https:// address."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

// --- approvals (WL-REQ-124) ----------------------------------------------

// decideApproval handles POST /approvals/{id}/decide, the cockpit's one
// decision act. It is registered behind requireSession, so the subject here
// was authenticated by a live session cookie: a bearer token and the
// open-instance subject never reach it, and there is deliberately no CLI verb
// and no /api/v1 route that decides an approval.
//
// The decision itself belongs to the store: DecideApproval checks the row is
// open, that the decider holds the group required_role names, and that they
// did not author the change under review. This handler reads the form, maps
// the outcome, and 303s back to the queue so a reload never decides twice.
func (s *server) decideApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.sameOriginForm(r) {
		s.observeApprovalDecision(decisionInvalid, decisionInvalid)
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return
	}
	if !parseWebForm(w, r) {
		s.observeApprovalDecision(decisionInvalid, decisionInvalid)
		return
	}
	decision := r.PostFormValue("decision")
	if _, ok := store.DecisionState(decision); !ok {
		s.observeApprovalDecision(decisionInvalid, decisionInvalid)
		webErr(w, http.StatusUnprocessableEntity,
			"choose a decision of approve, request changes, or reject")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.observeApprovalDecision(decision, "not_found")
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	sub := subjectFrom(r)
	err = s.recordEvent(ctx, "web", "approval.decided", map[string]any{
		"approval_id": id, "decision": decision, "actor": sub.ActorID,
	}, func(tx *sql.Tx, _ int64) error {
		_, err := store.DecideApproval(tx, store.DecideInput{
			ApprovalID: id,
			Decision:   decision,
			ActorID:    sub.ActorID,
			Groups:     sub.Groups,
			Now:        s.st.Now(),
		})
		return err
	})
	s.observeApprovalDecision(decision, approvalDecisionOutcome(err))
	if err != nil {
		s.approvalActErr(w, err)
		return
	}
	http.Redirect(w, r, decideReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// decideReturn is where a decided approval lands: the queue, or the page the
// form came from when it named one. The form field is attacker-controlled, so
// only a same-site absolute path is honoured — anything carrying a scheme, a
// host, or a second leading slash is an open redirect and falls back to the
// queue.
func decideReturn(want string) string {
	if want == "" || !strings.HasPrefix(want, "/") || strings.HasPrefix(want, "//") {
		return "/reviews"
	}
	u, err := url.Parse(want)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/reviews"
	}
	return want
}

// noteApproval handles POST /approvals/{id}/note, the dependent owner's note
// on an open impact review (WL-REQ-124): what the upstream change means for
// this entity, written down before a prior approver decides whether their
// approval still holds. Registered behind requireSession like the decide
// route, and it 303s back to the detail page the note is read on.
func (s *server) noteApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.sameOriginForm(r) {
		s.observeApprovalAct("note", decisionInvalid)
		webErr(w, http.StatusForbidden, "cross-origin form submissions are not accepted")
		return
	}
	if !parseWebForm(w, r) {
		s.observeApprovalAct("note", decisionInvalid)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.observeApprovalAct("note", "not_found")
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	sub := subjectFrom(r)
	err = s.recordEvent(ctx, "web", "approval.impact_noted", map[string]any{
		"approval_id": id, "actor": sub.ActorID,
	}, func(tx *sql.Tx, _ int64) error {
		return store.SetImpactNote(tx, id, r.PostFormValue("note"))
	})
	s.observeApprovalAct("note", approvalActOutcome(err))
	if err != nil {
		s.approvalActErr(w, err)
		return
	}
	http.Redirect(w, r, "/approvals/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// approvalActErr turns a refused act on an approval — a decision or an impact
// note — into the page the person sees. Each refusal names the rule that
// refused, since every one of them is something they can act on: wait for
// someone else, ask for the role, look at what the row already says, or
// designate a revision to review.
func (s *server) approvalActErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrApprovalResolved):
		webErr(w, http.StatusConflict, "this approval has already been decided")
	case errors.Is(err, store.ErrNoRevision):
		webErr(w, http.StatusUnprocessableEntity,
			"nothing has been designated for review yet")
	case errors.Is(err, store.ErrSelfApproval):
		webErr(w, http.StatusForbidden, "you authored this change, so you cannot decide it")
	case errors.Is(err, store.ErrNotQualified):
		webErr(w, http.StatusForbidden, "deciding this approval needs a role you do not hold")
	case errors.Is(err, store.ErrNotPriorApprover):
		webErr(w, http.StatusForbidden,
			"only someone who approved this entity can say whether that decision still holds")
	case errors.Is(err, store.ErrInvalidInput):
		webErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.webStoreErr(w, err)
	}
}

// --- page-script writes (WL-REQ-1338) -----------------------------------

// maxJSONPost caps a page-script write body at 64 KiB. These bodies name
// documents and plans; nothing legitimate approaches the cap.
const maxJSONPost = 64 << 10

// pageHeaderName and pageHeaderValue are the custom header the cockpit's page
// script sends on every write. An HTML form cannot set it, and a cross-origin script that tries
// triggers a preflight this server never answers, so it is a lock
// independent of the session cookie and of the origin check.
//
// sessionPageHeaderValue is the same header's value for a non-bearer /api/v1
// write (WL-REQ-1770), kept distinct from the cockpit's value so retiring
// one does not silently accept the other.
const (
	pageHeaderName         = "X-Requested-With"
	pageHeaderValue        = "lode-cockpit"
	sessionPageHeaderValue = "lode-web"
)

// sameOriginFetch is sameOriginForm's strict twin for script-issued writes
// (WL-REQ-1338 rule 2): where the form guard accepts Sec-Fetch-Site: none
// because a person may submit a form from a bookmark, a fetch has no such
// case — "none" is a user-typed navigation, which a fetch never is.
func (s *server) sameOriginFetch(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	return s.originAllowed(r)
}

// sessionWriteAllowed is apiGuard's CSRF gate for a non-bearer subject on a
// state-changing /api/v1 request (WL-REQ-1770): same origin, the page
// header, and — when the request carries a body — a JSON content type. It
// writes the refusal itself and returns false; the caller then returns.
//
// beginJSONPost will call this too once the cockpit's own page header is
// retired; until then it keeps its own checks and its "lode-cockpit" value
// unchanged.
func (s *server) sessionWriteAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.sameOriginFetch(r) {
		s.observeAPISessionRequest("refused_origin")
		writeErr(w, http.StatusForbidden, "cross-site request refused")
		return false
	}
	if r.Header.Get(pageHeaderName) != sessionPageHeaderValue {
		s.observeAPISessionRequest("refused_header")
		writeErr(w, http.StatusForbidden, "missing page header")
		return false
	}
	hasBody := r.ContentLength != 0 || len(r.TransferEncoding) > 0
	if hasBody {
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			s.observeAPISessionRequest("refused_content_type")
			writeErr(w, http.StatusUnsupportedMediaType, "the body must be application/json")
			return false
		}
	}
	return true
}

// beginJSONPost is the write gate for page-script requests (WL-REQ-1338): POST,
// strictly same-origin, the page's custom header, a JSON body, and the
// session's actor. It answers the refusal itself and returns ok false; the
// caller then returns. body is decoded into dst, which may be nil for a
// route that takes no fields.
//
// Every reply it writes, refusals included, carries the page's
// Content-Security-Policy and is JSON, never HTML (WL-REQ-1338 rule 4), so a
// navigation can never render one as a page. route is the metric label, not
// a path.
func (s *server) beginJSONPost(w http.ResponseWriter, r *http.Request, route string, dst any) (Subject, ui.CockpitProject, bool) {
	s.setWebHeaders(w)
	refuse := func(code int, msg string) (Subject, ui.CockpitProject, bool) {
		s.observeProgressWrite(route, "refused")
		writeErr(w, code, msg)
		return Subject{}, ui.CockpitProject{}, false
	}

	// Rule 1: no write on GET. SameSite=Lax still sends the cookie on a
	// top-level GET, so a mutating GET would be forgeable by a plain link.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		return refuse(http.StatusMethodNotAllowed, "this route accepts POST only")
	}
	if !s.sameOriginFetch(r) {
		return refuse(http.StatusForbidden, "cross-site request refused")
	}
	if r.Header.Get(pageHeaderName) != pageHeaderValue {
		return refuse(http.StatusForbidden, "missing page header")
	}
	// application/json is a non-simple content type, which forces the same
	// preflight for a cross-origin caller.
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return refuse(http.StatusUnsupportedMediaType, "the body must be application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONPost)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return refuse(http.StatusUnprocessableEntity, "the body could not be read")
	}
	// Read once into raw fields before decoding into dst, so a body naming
	// an actor is refused by the rule it breaks rather than as an unknown
	// field. Rule 6: the acting actor is the session's, never the body's.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return refuse(http.StatusUnprocessableEntity, "the body is not a JSON object")
	}
	if _, named := fields["actor"]; named {
		return refuse(http.StatusUnprocessableEntity, "the actor is the session")
	}
	if dst != nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(dst); err != nil {
			return refuse(http.StatusUnprocessableEntity, "the body could not be read: "+err.Error())
		}
	}

	project, err := s.projectHeader(r.Context(), r.PathValue("id"))
	if err != nil {
		s.observeProgressWrite(route, "refused")
		s.mapStoreErr(w, err)
		return Subject{}, ui.CockpitProject{}, false
	}
	return subjectFrom(r), project, true
}
