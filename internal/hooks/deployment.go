package hooks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// applyDeploymentStatus records a successful GitHub deployment as the
// gh-side watermark for the normalized environment, then advances every
// task covered by the new confirmed frontier.
func (h *githubHandler) applyDeploymentStatus(tx *sql.Tx, eventID int64, repo string, body []byte) error {
	var p struct {
		DeploymentStatus struct {
			State string `json:"state"`
		} `json:"deployment_status"`
		Deployment struct {
			Environment string `json:"environment"`
			SHA         string `json:"sha"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse deployment_status payload: %w", err)
	}
	if p.DeploymentStatus.State != "success" {
		return nil
	}
	env := store.NormalizeEnvironment(p.Deployment.Environment)
	if env == "" {
		return nil
	}
	mainID, err := store.MainIDForSHA(tx, repo, p.Deployment.SHA)
	if err != nil {
		return err
	}
	if mainID == nil {
		// The deployed sha has no main commit to anchor the watermark to, so
		// v1 drops this fact rather than queueing it. It self-heals: the next
		// deploy of this repo carries a sha we have seen and re-establishes
		// the frontier, which covers this one too.
		return nil
	}
	now := h.st.Now()
	if err := store.BumpEnvDeployGH(tx, now, repo, env, *mainID); err != nil {
		return err
	}
	return resolveFrontier(tx, now, repo, env, eventID)
}

// resolveFrontier re-reads the confirmed frontier for repo/env and resolves
// every task at or below it. Shared with the Flux handler.
func resolveFrontier(tx *sql.Tx, now time.Time, repo, env string, eventID int64) error {
	frontier, err := store.ConfirmedFrontier(tx, repo, env)
	if err != nil {
		return err
	}
	if frontier == nil {
		return nil
	}
	tasks, err := store.TasksBelowFrontier(tx, repo, *frontier)
	if err != nil {
		return err
	}
	for _, taskID := range tasks {
		if err := store.ResolveDelivery(tx, now, taskID, repo, eventID); err != nil {
			return err
		}
	}
	return nil
}
