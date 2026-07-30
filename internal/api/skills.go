package api

import (
	"context"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// recommendTimeout bounds the embedding-provider call on both the recommend
// endpoint and the task-brief path (recommendation() is shared with the
// brief — see Task 12); on expiry the response degrades to pins-only.
const recommendTimeout = 2 * time.Second

// skillSyncTimeout bounds one sync request: N tarball downloads (each up to
// 2 minutes, see githubauth.Tarball) plus serial per-skill embedding (each up
// to 30s, see embed.OpenAI's client timeout) can add up fast on a large
// corpus, so this is generous rather than tight.
const skillSyncTimeout = 5 * time.Minute

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
	// Content-addressed by hash in the URL, so the response can never go
	// stale under a fixed URL.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
		seen := map[string]bool{}
		for _, name := range pins {
			if seen[name] {
				continue // SkillsByNames dedupes the lookup, not the caller's list.
			}
			seen[name] = true
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

// syncResponse is skillsync.Summary plus, on a partial failure, the
// per-source error messages — the counts are real work done and must not be
// thrown away just because another source in the same request failed.
type syncResponse struct {
	Synced   int      `json:"synced"`
	Changed  int      `json:"changed"`
	Deleted  int      `json:"deleted"`
	Embedded int      `json:"embedded"`
	Errors   []string `json:"errors,omitempty"`
}

func (s *server) syncSkills(w http.ResponseWriter, r *http.Request) {
	if s.skillSyncer == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no skill sources configured (LODE_SKILL_SOURCES)")
		return
	}
	// Lock before deriving the timeout: a sync queued behind another must get
	// its full budget once it actually starts, not spend part of it waiting
	// here and then fail looking like a GitHub timeout.
	s.skillSyncMu.Lock()
	defer s.skillSyncMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), skillSyncTimeout)
	defer cancel()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	if err != nil {
		// Logged unconditionally: a caller that drops the response (or gets
		// the generic 502 below) must not be the only record of this.
		s.log.Error("skill sync failed", "err", err)
		if sum == (skillsync.Summary{}) {
			// SyncAll's contract: a zero Summary with an error means nothing
			// synced at all (e.g. the pre-sync embedding-invalidation check
			// failed). The detail is logged above, not leaked to the caller.
			writeErr(w, http.StatusBadGateway, "skill sync failed")
			return
		}
		// Partial failure: real work happened alongside the failures, so it
		// is reported rather than discarded.
		writeJSON(w, http.StatusOK, syncResponse{
			Synced: sum.Synced, Changed: sum.Changed, Deleted: sum.Deleted, Embedded: sum.Embedded,
			Errors: joinedMessages(err),
		})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// joinedMessages splits an errors.Join result back into its parts. SyncAll
// always returns one when it returns a non-nil error alongside a non-zero
// Summary, so this recovers the individual per-source failures instead of
// one run-on string.
func joinedMessages(err error) []string {
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		errs := u.Unwrap()
		out := make([]string, 0, len(errs))
		for _, e := range errs {
			out = append(out, e.Error())
		}
		return out
	}
	return []string{err.Error()}
}
