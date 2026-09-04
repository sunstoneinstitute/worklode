// deliverables.go serves spec 029 §3's deliverable over the JSON API and
// holds the validation and creation path the cockpit's web form shares with
// it (see webform.go), so a deliverable declared in a browser and one
// declared by an API client are the same write, recorded the same way, and
// differ only in the event source that records who typed it.
package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Field bounds for a declared deliverable. They exist to keep a stray paste
// out of the database and out of a cockpit list row, not to express domain
// meaning — spec 029 §3.1 puts no length on the three descriptive fields.
const (
	maxDeliverableName        = 200
	maxDeliverableDescription = 4000
	maxDeliverableURL         = 2000
	maxDeliverableArtifact    = 2000
)

// validateDeliverable trims and checks the declared fields, returning the
// cleaned input or a message naming the one thing to fix. Shared by the JSON
// handler and the web form so the two surfaces cannot drift into accepting
// different deliverables. milestone is trimmed only — existence and
// same-project containment (029 §2) are a store.CreateDeliverable check, not
// a validator concern.
func validateDeliverable(projectID, name, description, rawURL, artifact, milestone, createdBy string) (store.DeliverableInput, string) {
	in := store.DeliverableInput{
		ProjectID:   projectID,
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		URL:         strings.TrimSpace(rawURL),
		Artifact:    strings.TrimSpace(artifact),
		MilestoneID: strings.TrimSpace(milestone),
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
	case utf8.RuneCountInString(in.Artifact) > maxDeliverableArtifact:
		return in, "artifact is too long"
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
	// Artifact gets length only, deliberately: it is a catalog address the
	// ingest matches on, not a link anything renders. "bigquery://…",
	// "iceberg://…" and "gs://…" are all legal, and the comparison is exact
	// after this trim — no scheme or case normalisation, because dataset
	// identifiers are case-sensitive in the catalogs that report them.
	return in, ""
}

// validateArtifacts checks a list of catalog addresses to declare (PATCH
// /api/v1/tasks/{id} artifacts): each non-blank after trimming and within
// the same rune cap as a deliverable's artifact — length only, deliberately,
// for the reason validateDeliverable states. Returns "" when valid.
func validateArtifacts(artifacts []string) string {
	if len(artifacts) == 0 {
		return "artifacts must list at least one catalog address"
	}
	for _, a := range artifacts {
		a = strings.TrimSpace(a)
		switch {
		case a == "":
			return "artifacts must not contain a blank address"
		case utf8.RuneCountInString(a) > maxDeliverableArtifact:
			return "artifact is too long"
		}
	}
	return ""
}

// recordDeliverable writes one declared deliverable through RecordEvent, so
// the event log carries the fact and the source names the surface it came
// from ("cli" for the JSON API, "web" for a cockpit form).
func (s *server) recordDeliverable(ctx context.Context, source string, in store.DeliverableInput) (*model.Deliverable, error) {
	now := s.st.Now()

	var created *model.Deliverable
	if err := s.recordEvent(ctx, source, "deliverable.created", map[string]string{
		"project":     in.ProjectID,
		"name":        in.Name,
		"description": in.Description,
		"url":         in.URL,
		"artifact":    in.Artifact,
		"milestone":   in.MilestoneID,
		"created_by":  in.CreatedBy,
	}, func(tx *sql.Tx, _ int64) error {
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
	writeJSON(w, http.StatusOK, model.DeliverableListResponse{Deliverables: items})
}

// createDeliverable handles POST /api/v1/projects/{id}/deliverables.
func (s *server) createDeliverable(w http.ResponseWriter, r *http.Request) {
	var req model.CreateDeliverableInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	actorID := actorIDFrom(r)
	in, msg := validateDeliverable(projectID, req.Name, req.Description, req.URL, req.Artifact, req.Milestone, actorID)
	if msg != "" {
		writeErr(w, http.StatusUnprocessableEntity, msg)
		return
	}

	created, err := s.recordDeliverable(r.Context(), "cli", in)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// patchDeliverable handles PATCH /api/v1/deliverables/{id}: reparents a
// deliverable to another milestone in its own project, or detaches it
// ("" clears). The three descriptive fields stay immutable in P1 (spec 029
// §3.1), so milestone is the only field this route accepts.
func (s *server) patchDeliverable(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.EditDeliverableInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Milestone == nil {
		writeErr(w, http.StatusUnprocessableEntity, "no fields to update")
		return
	}

	err := s.recordEvent(r.Context(), "cli", "deliverable.updated", map[string]string{
		"deliverable": id,
		"milestone":   *req.Milestone,
	}, func(tx *sql.Tx, eventID int64) error {
		if err := store.SetDeliverableMilestone(tx, s.st.Now(), id, *req.Milestone); err != nil {
			return err
		}
		return store.LogChange(tx, "deliverable", id, eventID,
			map[string]string{"field": "milestone", "new": *req.Milestone})
	})
	s.observeMilestoneChange("deliverable_attach", err)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	d, err := s.st.GetDeliverable(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
