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
	"slices"
	"strconv"
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
func validateDeliverable(projectID, name, description, rawURL, artifact string, label bool, milestone, createdBy string) (store.DeliverableInput, string) {
	in := store.DeliverableInput{
		ProjectID:   projectID,
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		URL:         strings.TrimSpace(rawURL),
		Artifact:    strings.TrimSpace(artifact),
		Label:       label,
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
	case in.Artifact != "" && in.Label:
		// The store refuses this too (CreateDeliverable), but naming it here
		// gives the 422 a clean message before an event is recorded.
		return in, "declare an artifact address or a label, not both"
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
//
// It also materializes the review lanes the project's stamped approval flow
// demands of the new deliverable (029 §7.1), in the same transaction that
// records the creation. The hook lives here rather than in a handler so both
// surfaces get it: a deliverable declared in a browser owes the same reviews
// as one declared by an API client. A project with no snapshot is untouched.
func (s *server) recordDeliverable(ctx context.Context, source string, in store.DeliverableInput) (*model.Deliverable, error) {
	now := s.st.Now()

	var created *model.Deliverable
	materialized := 0
	if err := s.recordEvent(ctx, source, "deliverable.created", map[string]string{
		"project":     in.ProjectID,
		"name":        in.Name,
		"description": in.Description,
		"url":         in.URL,
		"artifact":    in.Artifact,
		"label":       strconv.FormatBool(in.Label),
		"milestone":   in.MilestoneID,
		"created_by":  in.CreatedBy,
	}, func(tx *sql.Tx, _ int64) error {
		d, err := store.CreateDeliverable(tx, now, in)
		if err != nil {
			return err
		}
		created = d
		snap, err := store.ProjectApprovalFlow(tx, in.ProjectID)
		if err != nil {
			return err
		}
		if snap == nil {
			return nil
		}
		materialized, err = store.MaterializeForEntity(tx, now, *snap, "deliverable", d.ID, d.Name)
		return err
	}); err != nil {
		return nil, err
	}
	s.observeApprovalRequirements(originFlow, materialized)
	return created, nil
}

// getDeliverable handles GET /api/v1/deliverables/{id}.
func (s *server) getDeliverable(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDeliverable(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
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
	in, msg := validateDeliverable(projectID, req.Name, req.Description, req.URL, req.Artifact, req.Label, req.Milestone, actorID)
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

// reportDeliverableState is the write both report surfaces share: one
// recorded event whose apply files user-reported evidence (029 §3.2), with
// the source naming the surface the person typed into. It counts the attempt
// on worklode_deliverable_reports_total itself, so neither caller can forget
// to, and returns the store's error for the caller to map to its own protocol.
func (s *server) reportDeliverableState(ctx context.Context, source, id, state, actor, note string) error {
	err := s.recordEvent(ctx, source, "deliverable.reported", map[string]string{
		"deliverable": id,
		"state":       state,
		"actor":       actor,
		"note":        note,
	}, func(tx *sql.Tx, eventID int64) error {
		return store.ReportDeliverableState(tx, eventID, s.st.Now(), id, state, source, actor, note)
	})
	s.observeDeliverableReport(source, deliverableReportOutcome(err))
	return err
}

// validReportState reports whether state is one the artifact_evidence CHECK
// accepts, and the message naming the five when it is not. Checked before the
// event is recorded so a typo leaves nothing in the log, and shared by both
// surfaces so neither accepts what the other rejects.
func validReportState(state string) (bool, string) {
	if slices.Contains(model.ArtifactStates, state) {
		return true, ""
	}
	return false, "state must be one of " + strings.Join(model.ArtifactStates, ", ")
}

// reportDeliverable handles POST /api/v1/deliverables/{id}/report: a person
// filing the state they see (029 §3.2). It writes evidence, never a column on
// the deliverable, and the evidence is user_reported — so the read projection
// keeps saying that a person claimed this rather than that anything observed
// it.
func (s *server) reportDeliverable(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ReportDeliverableInput
	if err := readJSON(w, r, &req); err != nil {
		s.observeDeliverableReport("cli", "invalid")
		writeBodyErr(w, err)
		return
	}
	if ok, msg := validReportState(req.State); !ok {
		s.observeDeliverableReport("cli", "invalid")
		writeErr(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if err := s.reportDeliverableState(r.Context(), "cli", id, req.State,
		actorIDFrom(r), strings.TrimSpace(req.Note)); err != nil {
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
