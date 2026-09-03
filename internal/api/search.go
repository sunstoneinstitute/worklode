// search.go is the HTTP surface over the corpus index's hybrid search
// (040 §9). The retrieval and its metrics live in store.Search; this handler
// only parses the query, embeds it, and reports how it actually answered
// (§11): a missing or failing embedding provider degrades every mode to
// lexical rather than erroring or returning nothing.
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// defaultSearchLimit applies when the caller asks for no particular number
// of hits; maxSearchLimit clamps an over-large ask rather than falling back
// to the default, matching defaultSkillLimit/maxSkillLimit in skills.go.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := q.Get("q")
	if text == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}

	limit := defaultSearchLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		if n > 0 {
			limit = n
		}
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	mode := q.Get("mode")
	if mode == "" {
		mode = model.SearchHybrid
	}
	provider := "none"
	if s.embedder != nil {
		provider = "openai-compatible"
	}

	// needsVector is only true for the two modes a vector could affect. An
	// unrecognized mode is left alone here and rejected by the store's own
	// validation (422), not silently rewritten.
	needsVector := mode == model.SearchHybrid || mode == model.SearchDense
	degraded := s.embedder == nil
	var vec []float32
	if s.embedder != nil && needsVector {
		ectx, cancel := context.WithTimeout(r.Context(), recommendTimeout)
		vecs, err := s.embedder.Embed(ectx, embed.RoleQuery, []string{embed.Truncate(text, embed.ChunkRunes)})
		cancel()
		if err != nil {
			// A failing provider degrades exactly like no provider at all
			// (§11): a 5xx here would turn a search into an outage.
			s.log.Error("search embedding failed", "err", err)
			degraded = true
		} else {
			vec = vecs[0]
		}
	}
	if degraded && needsVector {
		mode = model.SearchLexical
	}

	hits, err := s.st.Search(r.Context(), store.SearchQuery{
		Text: text, Vector: vec, Kinds: q["kind"], Project: q.Get("project"),
		Limit: limit, Mode: mode,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if hits == nil {
		hits = []model.SearchHit{}
	}
	writeJSON(w, http.StatusOK, model.SearchResponse{Provider: provider, Mode: mode, Hits: hits})
}
