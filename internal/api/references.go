// references.go serves spec 029 §5's entity_edges over the JSON API: typed
// references between entities of different kinds, the only edges allowed to
// cross a project boundary. It follows deliverables.go's shape — a
// record*/create* pair, RecordEvent wrapping the store write so the event log
// and the row commit together.
package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// recordReference writes one declared reference through RecordEvent, so the
// event log carries the fact and the source names the surface it came from,
// matching deliverables.go's recordDeliverable. The rel vocabulary and shape
// check live in store.CreateEntityEdge; this only wires the transaction.
func (s *server) recordReference(ctx context.Context, source string, in store.CreateEntityEdgeInput) (*model.EntityEdge, error) {
	now := s.st.Now()

	var created *model.EntityEdge
	err := s.recordEvent(ctx, source, "reference.created", map[string]string{
		"from_kind":  in.FromKind,
		"from":       in.From,
		"to_kind":    in.ToKind,
		"to":         in.To,
		"rel":        in.Rel,
		"created_by": in.CreatedBy,
	}, func(tx *sql.Tx, _ int64) error {
		e, err := store.CreateEntityEdge(tx, now, in)
		if err != nil {
			return err
		}
		created = e
		return nil
	})
	s.observeReferenceWrite(in.Rel, err)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// createReference handles POST /api/v1/references. The body is a
// model.EntityEdge, of which only the five identity fields are read;
// created_by is always the authenticated actor, never a caller-supplied
// value.
func (s *server) createReference(w http.ResponseWriter, r *http.Request) {
	var req model.EntityEdge
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	in := store.CreateEntityEdgeInput{
		FromKind:  req.FromKind,
		From:      req.From,
		ToKind:    req.ToKind,
		To:        req.To,
		Rel:       req.Rel,
		CreatedBy: actorIDFrom(r),
	}
	created, err := s.recordReference(r.Context(), "cli", in)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// listReferences handles GET /api/v1/references?kind=<k>&id=<id>: every
// reference touching the named entity, from either end. Both query
// parameters are required.
func (s *server) listReferences(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	id := r.URL.Query().Get("id")
	if kind == "" || id == "" {
		writeErr(w, http.StatusUnprocessableEntity, "kind and id are required")
		return
	}
	items, err := s.st.ReferencesFor(r.Context(), kind, id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ReferenceListResponse{References: items})
}
