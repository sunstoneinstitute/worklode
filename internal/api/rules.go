package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ruleRef reads the {id} path value as a rule ref such as WL-REQ-12; any
// infix resolves the rule by its number.
func ruleRef(w http.ResponseWriter, r *http.Request) (designdoc.RuleRef, bool) {
	ref, ok := designdoc.ParseRuleRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule id must look like WL-REQ-12")
		return designdoc.RuleRef{}, false
	}
	return ref, true
}

// getRule handles GET /api/v1/rules/{id}, where {id} is a rule ref
// such as WL-REQ-12.
func (s *server) getRule(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	c, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// editRule handles PUT /api/v1/rules/{id}: a rule-first write of the
// heading and body (S14, S35). The store regenerates the arranging
// document's body and writes it through the document path, so the event is
// recorded against that document.
func (s *server) editRule(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	var req model.EditRuleInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	current, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if len(current.ArrangedIn) != 1 {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("rule %s is arranged in %d documents; edit the document instead", current.Ref, len(current.ArrangedIn)))
		return
	}
	now := s.st.Now()
	err = s.recordDocEvent(w, r, "rule_edit", "doc.rule_edited", current.ArrangedIn[0].Doc, req,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.EditRule(tx, now, ref.Key, ref.Number, req, actorIDFrom(r), eventID)
			return err
		})
	if err != nil {
		return
	}
	c, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// patchRule handles PATCH /api/v1/rules/{id}: owner, tags and kind (S15). The
// text is PUT; this is the metadata write, the way docs split body from owner.
func (s *server) patchRule(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	var req model.RuleMetaInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	payload := map[string]any{"rule": r.PathValue("id"), "owner": req.Owner, "tags": req.Tags, "kind": req.Kind}
	err := s.recordEvent(r.Context(), "cli", "rule.updated", payload,
		func(tx *sql.Tx, _ int64) error {
			id, err := store.RuleIDByRef(tx, ref.Key, ref.Number)
			if err != nil {
				return err
			}
			return store.SetRuleMeta(tx, id, req)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	c, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// addRule handles POST /api/v1/rules: a standalone rule at draft version 1,
// owned by the caller and arranged in no document (WL-SPEC-77 §19.2).
func (s *server) addRule(w http.ResponseWriter, r *http.Request) {
	var req model.AddRuleInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	req.Project = strings.TrimSpace(req.Project)
	c, err := s.st.AddRule(r.Context(), req, actorIDFrom(r))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// acceptRule handles POST /api/v1/rules/{id}/accept: the rule's owner
// accepts its newest draft version (WL-SPEC-77 §19.2).
func (s *server) acceptRule(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	c, err := s.st.AcceptRule(r.Context(), ref.Key, ref.Number, actorIDFrom(r))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// arrangeRule handles POST /api/v1/docs/{id}/rules: place an existing rule
// in a spec, in place on a draft or in the candidate revision of an accepted
// one (WL-SPEC-77 §19.3). Answers with the rule.
func (s *server) arrangeRule(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	var req model.ArrangeRuleInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	ref, ok := designdoc.ParseRuleRef(req.Rule)
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule must look like WL-REQ-12")
		return
	}
	now := s.st.Now()
	if err := s.recordDocEvent(w, r, "arrange", "doc.rule_arranged", id, req,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.ArrangeRule(tx, now, id, req, actorIDFrom(r), eventID)
			return err
		}); err != nil {
		return
	}
	s.writeRule(w, r, ref)
}

// unarrangeRule handles DELETE /api/v1/docs/{id}/rules/{rule}: remove a rule
// from a spec without withdrawing it (WL-SPEC-77 §19.3).
func (s *server) unarrangeRule(w http.ResponseWriter, r *http.Request) {
	id, ok := docID(w, r)
	if !ok {
		return
	}
	rule := r.PathValue("rule")
	ref, ok := designdoc.ParseRuleRef(rule)
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule must look like WL-REQ-12")
		return
	}
	now := s.st.Now()
	if err := s.recordDocEvent(w, r, "unarrange", "doc.rule_unarranged", id, map[string]string{"rule": rule},
		func(tx *sql.Tx, eventID int64) error {
			return store.UnarrangeRule(tx, now, id, rule, actorIDFrom(r), eventID)
		}); err != nil {
		return
	}
	s.writeRule(w, r, ref)
}

// writeRule answers with a rule read back after a write.
func (s *server) writeRule(w http.ResponseWriter, r *http.Request, ref designdoc.RuleRef) {
	c, err := s.st.GetRule(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// listRuleVersions handles GET /api/v1/rules/{id}/versions.
func (s *server) listRuleVersions(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	vs, err := s.st.ListRuleVersions(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

// getRuleVersion handles GET /api/v1/rules/{id}/versions/{n}.
func (s *server) getRuleVersion(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= 0 {
		writeErr(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}
	c, err := s.st.GetRuleVersion(r.Context(), ref.Key, ref.Number, n)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// getRuleClosure handles GET /api/v1/rules/{id}/closure: the rule and every
// rule reachable over refines and needs (WL-SPEC-77 §4c).
func (s *server) getRuleClosure(w http.ResponseWriter, r *http.Request) {
	ref, ok := ruleRef(w, r)
	if !ok {
		return
	}
	c, err := s.st.RuleClosure(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// getRuleLint handles GET /api/v1/projects/{id}/rules/lint: the project's
// corpus against the targets of WL-SPEC-77 §4c.
func (s *server) getRuleLint(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	l, err := s.st.RuleLint(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// listRules handles GET /api/v1/rules. ?project= and ?status= narrow the
// list; ?doc= takes any document ref (WL-SPEC-73, a slug, an id) and returns
// that document's arrangement in order.
//
// No dedicated metric: 200, 404 on an unknown doc and 422 on a bad status or
// an ambiguous doc ref are http_requests_total's {route, code}.
func (s *server) listRules(w http.ResponseWriter, r *http.Request) {
	var p model.RuleListParams
	if err := readQuery(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f := store.RuleFilter{Project: p.Project, Status: p.Status}
	if ref := strings.TrimSpace(p.Doc); ref != "" {
		id, err := s.docIDByRef(r.Context(), ref)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		f.Doc = id
	}
	rules, err := s.st.ListRules(r.Context(), f)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// docIDByRef resolves a document ref the way GET /api/v1/docs/resolve does:
// an id or slug first, then the <KEY>-<TYPE>-<n> grammar. An ambiguous
// grammar ref is ErrInvalidInput.
func (s *server) docIDByRef(ctx context.Context, ref string) (int64, error) {
	d, err := s.st.ResolveDocRef(ctx, ref)
	if err == nil {
		return d.ID, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}
	gd, gerr := s.resolveDocRefWeb(ctx, ref, "")
	if gerr == nil {
		return gd.ID, nil
	}
	if amb := (*designdoc.AmbiguousRefError)(nil); errors.As(gerr, &amb) {
		return 0, fmt.Errorf("%s: %w", gerr.Error(), store.ErrInvalidInput)
	}
	return 0, err
}

// ruleRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var ruleRouteDocs = map[string]routeDoc{
	"GET /api/v1/rules": {
		summary:   "List rules, optionally filtered by project, status or document",
		responses: map[int]any{http.StatusOK: []model.Rule{}},
		params:    model.RuleListParams{},
	},
	"POST /api/v1/rules": {
		summary:   "Add a standalone rule, arranged in no document, at draft version 1",
		request:   model.AddRuleInput{},
		responses: map[int]any{http.StatusCreated: model.Rule{}},
	},
	"POST /api/v1/rules/{id}/accept": {
		summary:   "Accept a rule's newest draft version; owner only",
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"POST /api/v1/docs/{id}/rules": {
		summary:   "Arrange an existing rule in a spec; an accepted spec gets it in its candidate revision",
		request:   model.ArrangeRuleInput{},
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"DELETE /api/v1/docs/{id}/rules/{rule}": {
		summary:   "Remove a rule from a spec without withdrawing it; an accepted spec loses it in its candidate revision",
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"GET /api/v1/rules/{id}": {
		summary:   "Get a rule by ref",
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"PUT /api/v1/rules/{id}": {
		summary:   "Edit a rule's heading and body",
		request:   model.EditRuleInput{},
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"PATCH /api/v1/rules/{id}": {
		summary:   "Edit a rule's owner, tags and kind",
		request:   model.RuleMetaInput{},
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
	"GET /api/v1/rules/{id}/closure": {
		summary:   "Get a rule with its context closure over refines and needs",
		responses: map[int]any{http.StatusOK: model.RuleClosure{}},
	},
	"GET /api/v1/projects/{id}/rules/lint": {
		summary:   "Lint a project's rules against the corpus targets",
		responses: map[int]any{http.StatusOK: model.RuleLint{}},
	},
	"GET /api/v1/rules/{id}/versions": {
		summary:   "List a rule's versions",
		responses: map[int]any{http.StatusOK: []model.RuleVersion{}},
	},
	"GET /api/v1/rules/{id}/versions/{n}": {
		summary:   "Get a rule at one of its past versions",
		responses: map[int]any{http.StatusOK: model.Rule{}},
	},
}
