package api

import (
	"context"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// recommendTimeout bounds the embedding-provider call on the recommend
// path; on expiry the response degrades to pins-only.
const recommendTimeout = 2 * time.Second

type skillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceRepo  string `json:"source_repo"`
	Hash        string `json:"hash"`
	Deleted     bool   `json:"deleted"`
}

type skillMatchJSON struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Score       float64 `json:"score"`
}

type pinnedSkillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hash        string `json:"hash"`
	Content     string `json:"content"`
}

type recommendationJSON struct {
	Pinned   []pinnedSkillJSON `json:"pinned"`
	Matches  []skillMatchJSON  `json:"matches"`
	Warnings []string          `json:"warnings"`
	Provider string            `json:"provider"`
}

func toSkillJSON(sk store.Skill) skillJSON {
	return skillJSON{
		Name: sk.Name, Description: sk.Description, SourceRepo: sk.SourceRepo,
		Hash: sk.ContentHash, Deleted: sk.Deleted,
	}
}

func (s *server) listSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := s.st.ListSkills(r.Context(), r.URL.Query().Get("deleted") == "true")
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]skillJSON, 0, len(skills))
	for _, sk := range skills {
		out = append(out, toSkillJSON(sk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

func (s *server) getSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.st.GetSkill(r.Context(), r.PathValue("name"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSkillJSON(*sk))
}

func (s *server) skillArchive(w http.ResponseWriter, r *http.Request) {
	data, err := s.st.SkillArchive(r.Context(), r.PathValue("name"), r.PathValue("hash"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

type recommendRequest struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
	Limit  int    `json:"limit"`
}

func (s *server) recommendSkills(w http.ResponseWriter, r *http.Request) {
	var req recommendRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if (req.TaskID == "") == (req.Text == "") {
		writeErr(w, http.StatusUnprocessableEntity, "exactly one of task_id or text is required")
		return
	}
	var pins []string
	text := req.Text
	if req.TaskID != "" {
		task, err := s.st.GetTask(r.Context(), req.TaskID)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		text = task.Title + "\n\n" + task.Body
		// pins stays nil: tasks.skills lands in the task-pins commit (Task 8).
	}
	rec, err := s.recommendation(r.Context(), text, pins, req.Limit)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// recommendation resolves pins (inline content) and, when a provider is
// configured, embedding matches. Provider failures degrade to pins-only with
// a warning — recommendations never block work.
func (s *server) recommendation(ctx context.Context, text string, pins []string, limit int) (*recommendationJSON, error) {
	rec := &recommendationJSON{
		Pinned: []pinnedSkillJSON{}, Matches: []skillMatchJSON{}, Warnings: []string{},
		Provider: "none",
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	pinnedNames := map[string]bool{}
	if len(pins) > 0 {
		skills, err := s.st.SkillsByNames(ctx, pins)
		if err != nil {
			return nil, err
		}
		found := map[string]store.Skill{}
		for _, sk := range skills {
			found[sk.Name] = sk
		}
		for _, name := range pins {
			sk, ok := found[name]
			if !ok {
				rec.Warnings = append(rec.Warnings, "pinned skill not found: "+name)
				continue
			}
			if sk.Deleted {
				rec.Warnings = append(rec.Warnings, "pinned skill removed from its source repo: "+name)
			}
			pinnedNames[name] = true
			rec.Pinned = append(rec.Pinned, pinnedSkillJSON{
				Name: sk.Name, Description: sk.Description, Hash: sk.ContentHash, Content: sk.SkillMD,
			})
		}
	}

	if s.embedder == nil {
		return rec, nil
	}
	rec.Provider = "openai-compatible"
	ectx, cancel := context.WithTimeout(ctx, recommendTimeout)
	defer cancel()
	vecs, err := s.embedder.Embed(ectx, []string{embed.Truncate(text, embed.ChunkRunes)})
	if err != nil {
		rec.Warnings = append(rec.Warnings, "embedding provider unavailable; matches omitted")
		return rec, nil
	}
	matches, err := s.st.RecommendSkills(ctx, vecs[0], limit+len(pinnedNames), s.skillFloor)
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		if pinnedNames[m.Name] || len(rec.Matches) >= limit {
			continue
		}
		rec.Matches = append(rec.Matches, skillMatchJSON{
			Name: m.Name, Description: m.Description, Hash: m.ContentHash, Score: m.Score,
		})
	}
	return rec, nil
}

func (s *server) syncSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillSyncer == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no skill sources configured (LODE_SKILL_SOURCES)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s.skillSyncMu.Lock()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	s.skillSyncMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "skill sync failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}
