// deliverables.go serves spec 029 §3's deliverable over the JSON API and
// holds the validation and creation path the cockpit's web form shares with
// it (see webform.go), so a deliverable declared in a browser and one
// declared by an API client are the same write, recorded the same way, and
// differ only in the event source that records who typed it.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Field bounds for a declared deliverable. They exist to keep a stray paste
// out of the database and out of a cockpit list row, not to express domain
// meaning — spec 029 §3.1 puts no length on the three descriptive fields.
const (
	maxDeliverableName        = 200
	maxDeliverableDescription = 4000
	maxDeliverableURL         = 2000
)

// deliverableJSON is the wire form of a deliverable: every store.Deliverable
// field. There is no state field, and its absence is the point — spec 029
// §3.2 makes deliverable state a reported fact, so nothing here may look like
// a stored status.
type deliverableJSON struct {
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toDeliverableJSON(d *store.Deliverable) deliverableJSON {
	return deliverableJSON{
		ID:          d.ID,
		Project:     d.ProjectID,
		Name:        d.Name,
		Description: d.Description,
		URL:         d.URL,
		CreatedBy:   d.CreatedBy,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

type createDeliverableRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// validateDeliverable trims and checks the three descriptive fields, returning
// the cleaned input or a message naming the one thing to fix. Shared by the
// JSON handler and the web form so the two surfaces cannot drift into
// accepting different deliverables.
func validateDeliverable(projectID, name, description, rawURL, createdBy string) (store.DeliverableInput, string) {
	in := store.DeliverableInput{
		ProjectID:   projectID,
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		URL:         strings.TrimSpace(rawURL),
		CreatedBy:   createdBy,
	}
	// Counted in runes, not bytes, so the server and the field's HTML
	// maxlength agree about a name written in a non-Latin script.
	switch {
	case in.Name == "":
		return in, "name is required"
	case utf8.RuneCountInString(in.Name) > maxDeliverableName:
		return in, "name is too long"
	case utf8.RuneCountInString(in.Description) > maxDeliverableDescription:
		return in, "description is too long"
	case utf8.RuneCountInString(in.URL) > maxDeliverableURL:
		return in, "url is too long"
	}
	if in.URL != "" {
		// An absolute http(s) URL only. The deliverable's URL is rendered as a
		// link on a page other people read, so a "javascript:" or "data:"
		// address is rejected at the write rather than neutralized at every
		// read.
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return in, "url must be an absolute http or https address"
		}
	}
	return in, ""
}

// recordDeliverable writes one declared deliverable through RecordEvent, so
// the event log carries the fact and the source names the surface it came
// from ("cli" for the JSON API, "web" for a cockpit form).
func (s *server) recordDeliverable(ctx context.Context, source string, in store.DeliverableInput) (*store.Deliverable, error) {
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{
		"project":     in.ProjectID,
		"name":        in.Name,
		"description": in.Description,
		"url":         in.URL,
		"created_by":  in.CreatedBy,
	})
	if err != nil {
		return nil, err
	}
	now := s.st.Now()

	var created *store.Deliverable
	if _, _, err := s.st.RecordEvent(ctx, source, extID, "deliverable.created", payload,
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.CreateDeliverable(tx, now, in)
			if err != nil {
				return err
			}
			created = d
			return nil
		}); err != nil {
		return nil, err
	}
	return created, nil
}

// listProjectDeliverables handles GET /api/v1/projects/{id}/deliverables.
func (s *server) listProjectDeliverables(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	items, err := s.st.ListDeliverables(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]deliverableJSON, 0, len(items))
	for i := range items {
		out = append(out, toDeliverableJSON(&items[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliverables": out})
}

// createDeliverable handles POST /api/v1/projects/{id}/deliverables.
func (s *server) createDeliverable(w http.ResponseWriter, r *http.Request) {
	var req createDeliverableRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	actorID := ""
	if a := actorFrom(r); a != nil {
		actorID = a.ID
	}
	in, msg := validateDeliverable(projectID, req.Name, req.Description, req.URL, actorID)
	if msg != "" {
		writeErr(w, http.StatusUnprocessableEntity, msg)
		return
	}

	created, err := s.recordDeliverable(r.Context(), "cli", in)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDeliverableJSON(created))
}
