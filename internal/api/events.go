package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func toEventJSON(e store.Event) model.Event {
	return model.Event{
		ID:         e.ID,
		Source:     e.Source,
		ExternalID: e.ExternalID,
		Type:       e.Type,
		Payload:    json.RawMessage(e.Payload),
		ReceivedAt: e.ReceivedAt,
	}
}

func toEventSubscriberJSON(st store.EventSubscriberStatus) model.EventSubscriberStatus {
	return model.EventSubscriberStatus{
		Name:            st.Name,
		LastReadOffset:  st.LastRead,
		LastAckedOffset: st.LastAcked,
		Lag:             st.Lag,
		HolderPID:       st.HolderPID,
		UpdatedAt:       st.UpdatedAt,
	}
}

// listEvents handles GET /api/v1/events?type=&since=&after=&limit=. Any
// authenticated actor may read it (permEventRead).
func (s *server) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{Type: q.Get("type")}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid since: must be RFC3339")
			return
		}
		f.Since = t
	}
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid after: must be an integer event id")
			return
		}
		f.After = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid limit: must be a positive integer")
			return
		}
		f.Limit = n
	}

	events, err := s.st.ListEvents(r.Context(), f)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]model.Event, 0, len(events))
	for _, e := range events {
		out = append(out, toEventJSON(e))
	}
	writeJSON(w, http.StatusOK, model.EventListResponse{Events: out})
}

// listEventSubscribers handles GET /api/v1/event-subscribers. Any
// authenticated actor may read it (permEventRead).
func (s *server) listEventSubscribers(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.st.EventSubscriberStatuses(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]model.EventSubscriberStatus, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, toEventSubscriberJSON(st))
	}
	writeJSON(w, http.StatusOK, model.EventSubscriberListResponse{Subscribers: out})
}

// seekEventSubscriber handles POST /api/v1/event-subscribers/{name}/seek:
// an admin correction of consumer state (permEventAdmin), moving both
// offsets to the given position. Deliberately not wrapped in RecordEvent —
// nothing derives from subscriber offsets, and logging offset moves into the
// log they index would be noise.
func (s *server) seekEventSubscriber(w http.ResponseWriter, r *http.Request) {
	var req model.EventSubscriberSeekRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.To < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "to must not be negative")
		return
	}
	name := r.PathValue("name")

	if err := s.st.SeekEventSubscriber(r.Context(), name, req.To); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeEventSubscriberSeek(name)

	statuses, err := s.st.EventSubscriberStatuses(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	for _, st := range statuses {
		if st.Name == name {
			writeJSON(w, http.StatusOK, toEventSubscriberJSON(st))
			return
		}
	}
	// SeekEventSubscriber just proved the row exists; this would mean it was
	// deleted between the two calls, which nothing in this codebase does.
	s.mapStoreErr(w, store.ErrNotFound)
}
