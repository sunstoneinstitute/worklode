package cmd

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// completionTimeout bounds every completion lookup, scope resolution
// included: TAB must never hang the user's shell on a slow backbone
// (061 §3 C2). A test lowers it to force the deadline path.
var completionTimeout = 250 * time.Millisecond

// completionScope builds the client, deadline and project scope every
// completion helper needs. ok is false when anything the lookup depends on is
// missing — no config, no server URL, no resolvable project — and the caller
// then offers no candidates. It never reports why: a completion function's
// only channels to the user are the candidate list and the shell prompt
// itself (061 §3 C2).
func completionScope(cmd *cobra.Command) (ctx context.Context, cancel context.CancelFunc, c *cli.Client, scope cli.Scope, ok bool) {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return nil, nil, nil, cli.Scope{}, false
	}
	ctx, cancel = context.WithTimeout(cmd.Context(), completionTimeout)
	scope = currentScope(ctx, c, cfg)
	if scope.Project == "" {
		cancel()
		return nil, nil, nil, cli.Scope{}, false
	}
	return ctx, cancel, c, scope, true
}

// candidateTitleWidth truncates a task title so one candidate stays on one
// completion line — 40 runes leaves room for the id and a shell's own column
// budget without wrapping in a typical 80-column terminal (061 §3 C3).
const candidateTitleWidth = 40

// completionDescription sanitizes a free-text field for use as a completion
// candidate's description: a tab or newline in the title would corrupt the
// tab-separated "id\tdescription" line cobra and shells expect, so both are
// replaced with a space, and the result is truncated to candidateTitleWidth.
func completionDescription(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	runes := []rune(s)
	if len(runes) > candidateTitleWidth {
		return string(runes[:candidateTitleWidth-1]) + "…"
	}
	return s
}

// taskIDs completes a task-id argument from the tasks in the resolved
// project, ordered by model.CompareTaskIDs (061 §4) and carrying the task's
// title as a completion description ("WL-5\tfix the thing"), so a shell can
// render what a candidate is, not just its id (061 §3 C3). Any failure —
// offline, logged out, no project scope, server slower than
// completionTimeout — yields no candidates and no output, never
// cobra.ShellCompDirectiveError and never cobra.CompErrorln, both of which
// print into the prompt the user is typing.
func taskIDs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel, c, scope, ok := completionScope(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()
	resp, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: scope.Project})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	tasks := make([]model.Task, 0, len(resp.Tasks))
	for _, task := range resp.Tasks {
		if strings.HasPrefix(task.ID, toComplete) {
			tasks = append(tasks, task)
		}
	}
	// Sort the tasks, not the candidate strings: model.CompareTaskIDs parses
	// a bare id's numeric suffix, and a candidate already carries a
	// tab-joined title by the time it would be sorted (061 §4 vs. §3 C3).
	slices.SortFunc(tasks, func(a, b model.Task) int {
		return model.CompareTaskIDs(a.ID, b.ID)
	})
	ids := make([]cobra.Completion, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, cobra.Completion(task.ID+"\t"+completionDescription(task.Title)))
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}
