package api

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// flowActorID owns every approval row a flow rule mints (029 §7.2). The
// materializer stamps it as created_by, so the id is store's to define and
// this side only asserts the actor exists at boot.
const flowActorID = store.FlowActorID

//go:embed approvalflows/*.json
var defaultFlowFS embed.FS

// LoadApprovalFlows returns the effective flow set (029 §7.2): the embedded
// defaults, then every *.json in dir (LODE_APPROVAL_FLOWS_DIR; an empty dir
// string means defaults only). A dir flow whose name matches a default
// replaces it.
//
// Any unreadable or invalid file is a boot error, never a fallback — the
// instanceenv posture: a typo in configuration that changes what the server
// demands must fail startup rather than quietly demand something else.
func LoadApprovalFlows(dir string) ([]model.ApprovalFlow, error) {
	flows, err := readFlowDir(defaultFlowFS, "approvalflows")
	if err != nil {
		return nil, err
	}
	if dir != "" {
		override, err := readFlowDir(os.DirFS(dir), ".")
		if err != nil {
			return nil, err
		}
		flows = mergeFlows(flows, override)
	}
	return flows, nil
}

// readFlowDir parses every *.json directly under dir, in name order.
func readFlowDir(fsys fs.FS, dir string) ([]model.ApprovalFlow, error) {
	names, err := fs.Glob(fsys, filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("approval flows: %w", err)
	}
	if names == nil {
		// Glob reports no error for a missing directory, so ask explicitly:
		// a configured directory that is not there is a misconfiguration.
		if _, err := fs.Stat(fsys, dir); err != nil {
			return nil, fmt.Errorf("approval flows: %w", err)
		}
	}
	flows := make([]model.ApprovalFlow, 0, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("approval flow %s: %w", filepath.Base(name), err)
		}
		var f model.ApprovalFlow
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("approval flow %s: %w", filepath.Base(name), err)
		}
		if err := store.ValidateFlow(f); err != nil {
			return nil, fmt.Errorf("approval flow %s: %w", filepath.Base(name), err)
		}
		flows = append(flows, f)
	}
	return flows, nil
}

// flowNamed returns the loaded flow with this name, or nil. The set is read
// once at boot, so this is a scan over a handful of entries.
func (s *server) flowNamed(name string) *model.ApprovalFlow {
	for i := range s.flows {
		if s.flows[i].Name == name {
			return &s.flows[i]
		}
	}
	return nil
}

// applyApprovalFlow handles POST /api/v1/projects/{id}/approval-flow (029
// §7.2): stamp the named flow's snapshot on the project, and materialize the
// requirements it demands of the deliverables the project already holds. The
// stamp and the backfill share the transaction that records the event, so a
// project is never left carrying a flow whose rows were not written.
func (s *server) applyApprovalFlow(w http.ResponseWriter, r *http.Request) {
	var req model.ApplyApprovalFlowInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	projectID := r.PathValue("id")
	name := strings.TrimSpace(req.Name)
	if name == "" {
		s.observeApprovalFlowApply(flowApplyError)
		writeErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	// The flow vocabulary is instance configuration, not project data, so a
	// name nothing defines is a missing resource rather than a bad field.
	flow := s.flowNamed(name)
	if flow == nil {
		s.observeApprovalFlowApply(flowApplyUnknown)
		writeErr(w, http.StatusNotFound, "unknown approval flow: "+name)
		return
	}

	snap := model.ApprovalFlowSnapshot{Flow: *flow, Reviewers: req.Reviewers}
	materialized := 0
	err := s.recordEvent(r.Context(), "cli", "approval_flow.applied", map[string]string{
		"project":         projectID,
		"flow":            flow.Name,
		"rev":             flow.Rev,
		"materialized_by": store.FlowActorID,
	}, func(tx *sql.Tx, _ int64) error {
		if err := store.SetProjectApprovalFlow(tx, projectID, snap); err != nil {
			return err
		}
		n, err := store.MaterializeForProject(tx, s.st.Now(), projectID, snap)
		materialized = n
		return err
	})
	if err != nil {
		s.observeApprovalFlowApply(flowApplyError)
		s.mapStoreErr(w, err)
		return
	}
	s.observeApprovalFlowApply(flowApplyApplied)

	p, err := s.st.GetProject(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ApplyApprovalFlowResponse{
		Project: toProjectJSON(p, repos), Materialized: materialized,
	})
}

// mergeFlows overlays override onto base, matching on flow name.
func mergeFlows(base, override []model.ApprovalFlow) []model.ApprovalFlow {
	out := append([]model.ApprovalFlow(nil), base...)
	for _, o := range override {
		replaced := false
		for i := range out {
			if out[i].Name == o.Name {
				out[i], replaced = o, true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}
