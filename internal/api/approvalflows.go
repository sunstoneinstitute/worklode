package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// flowActorID owns every approval row a flow rule mints (029 §7.2): the rule
// inserted them, and crediting the policy to whichever human filed the idea
// would misstate who did what.
const flowActorID = "worklode"

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
