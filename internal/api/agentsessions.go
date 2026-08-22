package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// touchAgentSession handles POST /api/v1/tasks/{id}/agent-session: record
// that an agent session is working the task, or heartbeat an existing one.
// Only the lease holder may report; a non-holder gets 404, the same
// probe-resistant answer as renew.
func (s *server) touchAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.AgentSessionInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actorID := actorIDFrom(r)

	sess, err := s.st.TouchAgentSession(r.Context(), id, actorID,
		req.Agent, req.AgentVersion, req.SessionID, buckets)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// toUsageBuckets converts reported buckets to their store form, preserving
// nil (leave usage untouched) against empty (clear it).
//
// The day and the model are checked here so a client that garbles either gets
// a 400 naming the field, rather than the 422 the store's own validation
// would report from inside the write.
func toUsageBuckets(in []model.SessionUsageBucket) ([]store.SessionUsageBucket, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]store.SessionUsageBucket, 0, len(in))
	for _, b := range in {
		day, err := time.Parse(time.DateOnly, b.Day)
		if err != nil {
			return nil, fmt.Errorf("usage day %q: want YYYY-MM-DD", b.Day)
		}
		if b.Model == "" {
			return nil, fmt.Errorf("usage bucket for %s is missing model", b.Day)
		}
		out = append(out, store.SessionUsageBucket{
			Day:   day,
			Model: b.Model,
			Speed: b.Speed,
			Tokens: store.TokenCounts{
				Input:        b.InputTokens,
				CacheWrite5m: b.CacheWrite5mTokens,
				CacheWrite1h: b.CacheWrite1hTokens,
				CacheRead:    b.CacheReadTokens,
				Output:       b.OutputTokens,
			},
		})
	}
	return out, nil
}

// reportProjectOverheadUsage handles POST /api/v1/projects/{id}/overhead-usage:
// record usage with no task to bill to (spec 052 §2). All body fields are
// required; toUsageBuckets does the same day/model validation the
// task-scoped endpoints already use.
func (s *server) reportProjectOverheadUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ProjectOverheadUsageInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.ReportProjectOverheadUsage(r.Context(), id, req.Agent, req.ExternalSessionID, buckets); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// endAgentSession handles POST /api/v1/tasks/{id}/agent-session/end: close
// the caller's open session and record whatever usage it reports. Same
// holder policy as touchAgentSession (404 for a non-holder or an unknown
// task); all usage fields are optional, and an already-closed or unknown
// session is also a 404. A reported usage breakdown is priced by the store,
// which also rebuilds the project's daily rollup from it.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.EndAgentSessionInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	actorID := actorIDFrom(r)

	err = s.st.EndAgentSession(r.Context(), id, actorID, req.Agent, req.SessionID,
		store.SessionUsage{
			InputTokens:  req.InputTokens,
			OutputTokens: req.OutputTokens,
			CostAmount:   req.CostAmount,
			CostCurrency: req.CostCurrency,
			Buckets:      buckets,
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
