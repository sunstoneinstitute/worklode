// Task-scoped token minting (001 §2.1, WL-306): POST /api/v1/tasks/{id}/tokens
// mints a wl_ token bound to the task, attributed to an agent actor, and
// expiring with the task's lease — the just-in-time credential a sandbox
// works with instead of the operator's own. Scoping is enforced by the
// router (requireTaskScope); revocation and extension ride the lease
// lifecycle in internal/store.

package api

import (
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// defaultTaskTokenActor is the agent actor a minted token acts as when the
// caller names none. Auto-provisioned (kind agent, never admin) so the first
// mint on a fresh instance needs no setup.
const defaultTaskTokenActor = "sandbox"

// maxTaskTokenTTL bounds a requested TTL: a task-scoped token outliving any
// plausible lease is an actor-scoped token wearing a costume.
const maxTaskTokenTTL = 24 * time.Hour

// mintTaskToken handles POST /api/v1/tasks/{id}/tokens.
func (s *server) mintTaskToken(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var req model.TaskTokenInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = store.DefaultLeaseTTL
	}
	if ttl > maxTaskTokenTTL {
		writeErr(w, http.StatusUnprocessableEntity, "ttl_seconds is longer than a task token may live (24h)")
		return
	}

	if _, err := s.st.GetTask(r.Context(), taskID); err != nil {
		s.observeTaskToken("not_found")
		s.mapStoreErr(w, err)
		return
	}

	actor := req.Actor
	if actor == "" {
		actor = defaultTaskTokenActor
		if err := s.st.EnsureActor(r.Context(), actor, "agent", "Sandbox worker"); err != nil {
			s.observeTaskToken("error")
			s.mapStoreErr(w, err)
			return
		}
	} else if _, err := s.st.GetActor(r.Context(), actor); err != nil {
		s.observeTaskToken("not_found")
		s.mapStoreErr(w, err)
		return
	}

	expiresAt := s.st.Now().Add(ttl)
	plaintext, err := s.st.CreateTaskToken(r.Context(), actor, taskID,
		"task-scoped token minted by "+actorIDFrom(r), expiresAt)
	if err != nil {
		s.observeTaskToken("error")
		s.mapStoreErr(w, err)
		return
	}
	s.observeTaskToken("ok")
	writeJSON(w, http.StatusCreated, model.TaskTokenResponse{
		Token: plaintext, Actor: actor, Task: taskID, ExpiresAt: expiresAt.UTC(),
	})
}
