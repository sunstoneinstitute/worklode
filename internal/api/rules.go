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

// ruleRef reads the {id} path value as a rule ref such as WL-RULE-12.
func ruleRef(w http.ResponseWriter, r *http.Request) (designdoc.RuleRef, bool) {
	ref, ok := designdoc.ParseRuleRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule id must look like WL-RULE-12")
		return designdoc.RuleRef{}, false
	}
	return ref, true
}

// getRule handles GET /api/v1/rules/{id}, where {id} is a rule ref
// such as WL-RULE-12.
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

// patchRule handles PATCH /api/v1/rules/{id}: owner and tags (S15). The
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
	payload := map[string]any{"rule": r.PathValue("id"), "owner": req.Owner, "tags": req.Tags}
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

// listRules handles GET /api/v1/rules. ?project= and ?status= narrow the
// list; ?doc= takes any document ref (WL-SPEC-73, a slug, an id) and returns
// that document's arrangement in order.
//
// No dedicated metric: 200, 404 on an unknown doc and 422 on a bad status or
// an ambiguous doc ref are http_requests_total's {route, code}.
func (s *server) listRules(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.RuleFilter{Project: q.Get("project"), Status: q.Get("status")}
	if ref := strings.TrimSpace(q.Get("doc")); ref != "" {
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
