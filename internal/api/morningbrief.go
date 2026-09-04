package api

import (
	"encoding/json"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// morningBriefTier is 032 §9's ordering, from the spec's own numbered list.
// Tier 1 (decisions and exceptions needing the actor) is never assigned to
// an event: it is derived from open state in assembleMorningBrief, which is
// what makes it persist across cursor advances.
type morningBriefTier int

const (
	briefTierOutcome morningBriefTier = 2 // material outcomes and changes
	briefTierStopped morningBriefTier = 3 // runs that stopped or reached a bound
	briefTierRoutine morningBriefTier = 4 // routine successful work, collapsed
)

// morningBriefTierOf classifies one event type per the pinned table in the
// plan's Global Constraints. Unknown types are routine — the default that
// keeps a new producer from paging a human by accident.
func morningBriefTierOf(eventType string) morningBriefTier {
	switch eventType {
	case "task.stopped", "task.abandoned", "lease.expired",
		"runtime.crashloop", "runtime.oom", "runtime.flux_failure":
		return briefTierStopped
	case "wl:DocumentSubmitted", "wl:DocumentAccepted", "task.done",
		"task.reopened", "deliverable.created", "approval.decided",
		"crew.member_added", "crew.member_removed", "issue.promoted",
		"runtime.flux_recovery":
		return briefTierOutcome
	default:
		return briefTierRoutine
	}
}

// morningBriefProject attributes one event to a project: payload "project",
// else the "<KEY>-" prefix of payload "task" via keyToProject, else the
// payload's repository.full_name via repoToProject. "" means unattributed —
// the caller drops the event from the brief.
func morningBriefProject(ev store.Event, keyToProject, repoToProject map[string]string) string {
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return ""
	}

	if project, ok := payload["project"].(string); ok && project != "" {
		return project
	}

	if taskID, ok := payload["task"].(string); ok {
		if i := strings.IndexByte(taskID, '-'); i >= 0 {
			if project, ok := keyToProject[taskID[:i]]; ok {
				return project
			}
		}
	}

	if repository, ok := payload["repository"].(map[string]any); ok {
		if fullName, ok := repository["full_name"].(string); ok {
			if project, ok := repoToProject[fullName]; ok {
				return project
			}
		}
	}

	return ""
}
