package api

import (
	"context"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// recommendTimeout bounds the embedding-provider call on both the recommend
// endpoint and the task-brief path, which share skillMatches; on expiry the
// response degrades to pins-only.
const recommendTimeout = 2 * time.Second

// defaultSkillLimit applies when the caller asks for no particular number of
// matches; maxSkillLimit clamps an over-large ask. Clamping, not falling back
// to the default: "--limit 50" asked for more, so returning fewer than
// "--limit 20" would have is the one answer that cannot be right.
const (
	defaultSkillLimit = 5
	maxSkillLimit     = 20
)

// skillSyncTimeout bounds one sync request: N tarball downloads (each up to
// 2 minutes, see githubauth.Tarball) plus serial per-skill embedding (each up
// to 30s, see embed.OpenAI's client timeout) can add up fast on a large
// corpus, so this is generous rather than tight.
const skillSyncTimeout = 5 * time.Minute

func toSkillJSON(sk store.Skill) model.Skill {
	return model.Skill{
		Name: sk.Name, Description: sk.Description, SourceRepo: sk.SourceRepo,
		Hash: sk.ContentHash, Deleted: sk.Deleted,
	}
}

func toPinnedSkillJSON(sk store.Skill) model.PinnedSkill {
	return model.PinnedSkill{
		Name: sk.Name, Description: sk.Description, Hash: sk.ContentHash, Content: sk.SkillMD,
	}
}

func (s *server) listSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := s.st.ListSkills(r.Context(), r.URL.Query().Get("deleted") == "true")
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]model.Skill, 0, len(skills))
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

func (s *server) recommendSkills(w http.ResponseWriter, r *http.Request) {
	var req model.RecommendInput
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
		pins = task.Skills
	}
	rec, err := s.recommendation(r.Context(), text, pins, req.Limit)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// recommendation resolves pins (inline content) and, when a provider is
// configured, embedding matches excluding the pinned names. Provider
// failures degrade to pins-only with a warning — recommendations never block
// work. Matching itself is shared with the task brief via skillMatches: the
// brief already has its pins resolved by store.Brief, so taskBrief calls
// skillMatches directly instead of re-resolving them here.
func (s *server) recommendation(ctx context.Context, text string, pins []string, limit int) (*model.SkillRecommendation, error) {
	rec := &model.SkillRecommendation{
		Pinned: []model.PinnedSkill{}, Matches: []model.SkillMatch{}, Warnings: []string{},
		Provider: "none",
	}

	pinnedNames := map[string]bool{}
	if len(pins) > 0 {
		// Store.ResolvePins is the one implementation, shared with the brief
		// (store.Brief resolves its own pins through it). Duplicating the
		// dedupe and the warning strings here is how the two drift apart.
		pinned, warnings, err := s.st.ResolvePins(ctx, pins)
		if err != nil {
			return nil, err
		}
		rec.Warnings = append(rec.Warnings, warnings...)
		for _, sk := range pinned {
			pinnedNames[sk.Name] = true
			rec.Pinned = append(rec.Pinned, toPinnedSkillJSON(sk))
		}
	}

	if s.embedder != nil {
		rec.Provider = "openai-compatible"
	}
	matches, warnings := s.skillMatches(ctx, text, pinnedNames, limit)
	rec.Matches = matches
	rec.Warnings = append(rec.Warnings, warnings...)
	return rec, nil
}

// skillMatches computes embedding matches for text, excluding any name in
// exclude (typically already-pinned skills, so the same skill is never
// surfaced twice). It is the one embedding code path shared by recommendation
// (pins resolved just above) and the task brief handler (pins already
// resolved by store.Brief).
//
// It never fails. Every way matching can break — provider down, provider
// timeout past recommendTimeout, or a vector query the store rejects (mixed
// dimensions in the corpus make every cosine query error) — degrades to no
// matches plus a warning. Pins-only is a fully functional mode per spec 016,
// and this path serves the task brief: an error here would stop anyone from
// starting work.
func (s *server) skillMatches(ctx context.Context, text string, exclude map[string]bool, limit int) ([]model.SkillMatch, []string) {
	matches := []model.SkillMatch{}
	switch {
	case limit <= 0:
		limit = defaultSkillLimit
	case limit > maxSkillLimit:
		limit = maxSkillLimit
	}
	if s.embedder == nil {
		return matches, nil
	}
	ectx, cancel := context.WithTimeout(ctx, recommendTimeout)
	defer cancel()
	vecs, err := s.embedder.Embed(ectx, []string{embed.Truncate(text, embed.ChunkRunes)})
	if err != nil {
		return matches, []string{"embedding provider unavailable; matches omitted"}
	}
	found, err := s.st.RecommendSkills(ctx, vecs[0], limit+len(exclude), s.skillFloor)
	if err != nil {
		// Logged: the caller gets a warning, but the cause (usually a corpus
		// left at two dimensions) is only visible here.
		s.log.Error("skill match query failed", "err", err)
		return matches, []string{"skill matching unavailable; matches omitted"}
	}
	for _, m := range found {
		if exclude[m.Name] || len(matches) >= limit {
			continue
		}
		matches = append(matches, model.SkillMatch{
			Name: m.Name, Description: m.Description, Hash: m.ContentHash, Score: m.Score,
		})
	}
	return matches, nil
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
	// TryLock, not Lock: a background sync (webhook push or boot) can hold
	// this mutex for up to skillSyncTimeout. Blocking the admin request that
	// long and then failing with a 502 that looks like a GitHub problem is
	// worse than an honest, immediate 409.
	if !s.skillSyncMu.TryLock() {
		writeErr(w, http.StatusConflict, "skill sync already running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), skillSyncTimeout)
	defer cancel()
	start := time.Now()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	s.observeSkillSync(sum, err, time.Since(start))
	// A webhook push that arrived while this ran found the mutex held, set
	// skillSyncPending and returned. runSkillSync's loop drains that flag; this
	// handler holds the same mutex without one, so it has to hand the trigger
	// on or the push is dropped until the next one. Unlock first: runSkillSync
	// TryLocks, and would otherwise just re-set the flag nobody is left to
	// drain.
	s.skillSyncMu.Unlock()
	if s.skillSyncPending.CompareAndSwap(true, false) {
		go s.runSkillSync(s.bgCtx, "coalesced after admin sync")
	}
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
