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
// response degrades to the lexical arm alone.
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

// Both conversions send the qualified name (<plugin>:<name>), not the bare
// one: it is the registry's identity, what a pin should name, and what a
// client stores the skill under locally — two plugins' same-named skills
// would otherwise land on one path.

func toSkillJSON(sk store.Skill) model.Skill {
	return model.Skill{
		Name: sk.QualifiedName(), Description: sk.Description, SourceRepo: sk.SourceRepo,
		Hash: sk.ContentHash, Deleted: sk.Deleted,
	}
}

func toPinnedSkillJSON(sk store.Skill) model.PinnedSkill {
	return model.PinnedSkill{
		Name: sk.QualifiedName(), Description: sk.Description, Hash: sk.ContentHash, Content: sk.SkillMD,
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
	writeJSON(w, http.StatusOK, model.SkillsListResponse{Skills: out})
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

// recommendation resolves pins (inline content) and retrieval matches
// excluding the pinned names. A provider failure degrades to the lexical arm
// with a warning, and no provider at all to pins plus lexical matches (040
// §11) — recommendations never block work. Matching itself is shared with
// the task brief via skillMatches: the
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
			pinnedNames[sk.QualifiedName()] = true
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

// skillMatches retrieves skill matches for text, excluding any name in
// exclude (typically already-pinned skills, so the same skill is never
// surfaced twice). Both sides of that comparison are qualified names — a
// bare-name exclusion set would silently stop excluding anything. It is the
// one retrieval path shared by recommendation (pins resolved just above) and
// the task brief handler (pins already resolved by store.Brief).
//
// Retrieval is store.Search with kind=skill (040 §9): recommendation is a
// caller of the one hybrid path rather than a second embedding code path of
// its own, which is what gives it the lexical arm — a brief naming a tool
// literally now matches the skill that names it back.
//
// It never fails. Every way matching can break degrades to fewer matches
// plus a warning, never an error: no provider or a provider that is down or
// slower than recommendTimeout drops to the lexical arm alone (§11), and a
// query the store rejects drops to no matches at all. Pins-only is a fully
// functional mode per spec 016, and this path serves the task brief: an
// error here would stop anyone from starting work.
func (s *server) skillMatches(ctx context.Context, text string, exclude map[string]bool, limit int) ([]model.SkillMatch, []string) {
	matches := []model.SkillMatch{}
	if limit <= 0 {
		limit = defaultSkillLimit
	} else if limit > maxSkillLimit {
		limit = maxSkillLimit
	}

	var warnings []string
	var vec []float32
	if s.embedder != nil {
		ectx, cancel := context.WithTimeout(ctx, recommendTimeout)
		vecs, err := s.embedder.Embed(ectx, embed.RoleQuery, []string{embed.Truncate(text, embed.ChunkRunes)})
		cancel()
		if err != nil {
			warnings = append(warnings, "embedding provider unavailable; lexical matches only")
		} else {
			vec = vecs[0]
		}
	}
	// No vector means the dense arm cannot run, so ask for the mode that
	// actually applies rather than letting hybrid quietly answer with one arm.
	mode := model.SearchHybrid
	if len(vec) == 0 {
		mode = model.SearchLexical
	}
	hits, err := s.st.Search(ctx, store.SearchQuery{
		Text: text, Vector: vec, Kinds: []string{store.SubjectSkill},
		Limit: limit + len(exclude), Mode: mode,
	})
	if err != nil {
		// Logged: the caller gets a warning, but the cause (usually a query
		// vector the stored ones cannot be compared against) is only visible
		// here.
		s.log.Error("skill match query failed", "err", err)
		return matches, append(warnings, "skill matching unavailable; matches omitted")
	}
	if len(hits) == 0 {
		return matches, warnings
	}

	// A hit carries the skill's qualified name but not its description or
	// content hash, which the response body needs. Resolving the names is one
	// extra query, against the same rule 1 exact match a pin uses.
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.Title)
	}
	res, err := s.st.ResolveSkillRefs(ctx, names)
	if err != nil {
		s.log.Error("skill match resolution failed", "err", err)
		return matches, append(warnings, "skill matching unavailable; matches omitted")
	}
	byName := make(map[string]store.Skill, len(res))
	for _, r := range res {
		if r.Skill != nil {
			byName[r.Skill.QualifiedName()] = *r.Skill
		}
	}

	for _, h := range hits {
		if exclude[h.Title] || len(matches) >= limit {
			continue
		}
		sk := byName[h.Title]
		matches = append(matches, model.SkillMatch{
			Name: h.Title, Description: sk.Description, Hash: sk.ContentHash,
			// Score is the fused reciprocal-rank sum store.Search returns, not
			// a cosine similarity (040 §6.1). It orders matches; it does not
			// measure them.
			Score: h.Score,
		})
	}
	return matches, warnings
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
	report := model.SkillSyncReport{
		Synced: sum.Synced, Changed: sum.Changed, Deleted: sum.Deleted,
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
		report.Errors = joinedMessages(err)
	}
	writeJSON(w, http.StatusOK, report)
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
