// The task-id autolink's server half (WL-305): the project-key set the
// renderer needs comes from the store, so only a store-backed test proves the
// wiring — internal/mdrender's own tests cover the pattern.

package api_test

import (
	"strings"
	"testing"
)

// TestTaskPageLinksBareTaskIDs: a body mentioning another task's id renders
// that id as a link to its page, while an acronym of the same shape stays
// text because its key is not a project key.
func TestTaskPageLinksBareTaskIDs(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj") // key "WL"
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Target", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Mentions", "priority": "medium", "kind": "feature",
		"body": "Follows WL-1, encoded UTF-8, and ZZQ-3 is nobody's project.",
	})

	page := doReq(t, h, "GET", "/tasks/WL-2", "", nil).Body.String()
	if !strings.Contains(page, `href="/tasks/WL-1"`) {
		t.Errorf("bare task id not autolinked:\n%s", page)
	}
	for _, unwanted := range []string{"/tasks/UTF-8", "/tasks/ZZQ-3"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("linked %s, which is not a task id:\n%s", unwanted, page)
		}
	}
}
