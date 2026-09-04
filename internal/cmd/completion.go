package cmd

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// completionTimeout bounds every completion lookup, scope resolution
// included: TAB must never hang the user's shell on a slow backbone
// (061 §3 C2). A test lowers it to force the deadline path.
var completionTimeout = 250 * time.Millisecond

// completionClient builds the client and deadline every completion lookup
// needs, and nothing more. ok is false when there is no usable config or no
// server URL, and the caller then offers no candidates. It never reports why:
// a completion function's only channels to the user are the candidate list
// and the shell prompt itself (061 §3 C2).
//
// It is separate from completionScope because not every lookup is
// project-scoped. GET /api/v1/projects is global, and requiring a resolved
// project before completing a project argument would offer nothing in exactly
// the case that needs it most: a checkout with no project scoped.
func completionClient(cmd *cobra.Command) (context.Context, context.CancelFunc, *cli.Client, cli.Config, bool) {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return nil, nil, nil, cli.Config{}, false
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	return ctx, cancel, c, cfg, true
}

// completionScope is completionClient plus the resolved project every
// project-scoped lookup needs. ok is false for completionClient's reasons and
// additionally when no project resolves.
func completionScope(cmd *cobra.Command) (ctx context.Context, cancel context.CancelFunc, c *cli.Client, scope cli.Scope, ok bool) {
	ctx, cancel, c, cfg, ok := completionClient(cmd)
	if !ok {
		return nil, nil, nil, cli.Scope{}, false
	}
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

// taskIDAt completes a task id at argument position n (0-based). Which
// position holds the id is a property of the command being wired, not of the
// lookup: `lode task attach <task-id> <file>...` takes it first,
// `lode inbox link <repo> <number> <task-id>` third. Any other position falls
// through to the shell's own default, so wiring an id argument never takes
// away the filename completion the arguments after it already had.
func taskIDAt(n int) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) != n {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return taskIDs(cmd, args, toComplete)
	}
}

// staticCompletions offers a closed value set: the kinds, statuses,
// priorities and `set` field names of 061 §3 C4. Only the values starting
// with what has been typed are offered, and nothing falls through to filename
// completion behind them. The prefix match belongs here because cobra hands a
// completion function's result to the shell unfiltered — unlike ValidArgs,
// which it narrows itself.
func staticCompletions(values []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out := make([]cobra.Completion, 0, len(values))
	for _, v := range values {
		if strings.HasPrefix(v, toComplete) {
			out = append(out, cobra.Completion(v))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeFlagValues registers a closed value set as the completion of cmd's
// named flag. RegisterFlagCompletionFunc's only error is a misspelled or
// twice-registered flag name — a wiring mistake, which shows up as a flag
// that completes nothing rather than as anything a user could hit at runtime.
func completeFlagValues(cmd *cobra.Command, flag string, values []string) {
	_ = cmd.RegisterFlagCompletionFunc(flag, func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return staticCompletions(values, toComplete)
	})
}

// completeProjectFlag registers the live project lookup on cmd's named
// project flag, for addScopeFlags' --project and the two commands that spell
// the flag themselves.
func completeProjectFlag(cmd *cobra.Command, flag string) {
	_ = cmd.RegisterFlagCompletionFunc(flag, projectKeys)
}

// taskSetArgs completes `lode task set <field> <value…> <id>` (061 §2.1)
// position by position. What belongs at a position is a property of the field
// named first, so the field decides: after "state" comes one of the settable
// states and then the id, after "checklist" an item and a true/false and then
// the id, and after "skills" any number of skill names, or none at all, and
// then the id.
//
// Dispatching on the field is what lets the documented clear form
// (`lode task set skills WL-5`, the id at position 1) complete. A single
// position threshold could not: position 1 holds a state for one field and
// the id for another. Skill names are still offered nothing of their own —
// the pinned-skill argument has no listing behind it — so from position 1 on
// a "skills" set offers ids, which is the whole candidate set that grammar
// admits there.
func taskSetArgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return staticCompletions(taskSetFields, toComplete)
	}
	switch args[0] {
	case "state":
		if len(args) == 1 {
			return staticCompletions(model.SettableTaskStates, toComplete)
		}
	case "checklist":
		switch len(args) {
		case 1:
			// The item: an ordinal or a title, neither of them completable.
			return nil, cobra.ShellCompDirectiveNoFileComp
		case 2:
			return staticCompletions([]string{"false", "true"}, toComplete)
		}
	}
	return taskIDs(cmd, args, toComplete)
}

// docRefsFiltered is the shared body of the document-reference completers.
// A document is addressable several ways (026 §4.2): by slug
// ("design-doc-queries"), by the 025 §14.3 shorthand ("WL-SPEC-61"), by a
// number-and-slug form, by a corpus path, and by any of those with a
// "#sec-N" fragment. Slug and shorthand are offered — the two forms a person
// types from memory. Fragments are deliberately not attempted here: a
// fragment's candidates are the target document's own anchors, which needs a
// second, per-document lookup keyed on a reference that is still half-typed.
// If that is ever wanted it is its own task, not a line in this one.
//
// deleted selects the tombstoned corpus instead of the live one, which is
// the only useful candidate set for `lode doc undelete` (044 §5).
func docRefsFiltered(cmd *cobra.Command, toComplete string, deleted bool) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel, c, scope, ok := completionScope(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()
	resp, _, err := c.ListDocs(ctx, cli.DocListFilter{Project: scope.Project, Deleted: deleted})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	docs := resp.Docs
	// Sort the documents, not the candidate strings, for taskIDs' reason:
	// the shorthand's numeric suffix has to be compared as a number, and a
	// candidate already carries a tab-joined title by the time it would sort.
	// CompareTaskIDs reads "WL-SPEC-9" as key "WL-SPEC" and number 9, so one
	// comparator orders both id shapes.
	slices.SortFunc(docs, func(a, b model.Doc) int {
		return model.CompareTaskIDs(cli.DocRef(a), cli.DocRef(b))
	})
	out := make([]cobra.Completion, 0, 2*len(docs))
	for _, d := range docs {
		desc := completionDescription(d.Title)
		refs := []string{d.Slug}
		// A document with no number predates 029 §4's backfill; DocRef
		// degrades it to a bare kind ("spec"), which is not a reference.
		if d.Number != 0 {
			refs = append([]string{cli.DocRef(d)}, refs...)
		}
		for _, ref := range refs {
			if ref != "" && strings.HasPrefix(ref, toComplete) {
				out = append(out, cobra.Completion(ref+"\t"+desc))
			}
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// docRefs completes a document reference at any position, for the one command
// whose refs are variadic: `lode doc transfer [ref...]`.
func docRefs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return docRefsFiltered(cmd, toComplete, false)
}

// docRefAt completes a document reference at argument position n, with
// taskIDAt's position discipline: any other position falls through to the
// shell's own default so a later filename argument keeps completing.
func docRefAt(n int) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) != n {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return docRefsFiltered(cmd, toComplete, false)
	}
}

// deletedDocRefAt is docRefAt over the tombstoned corpus, for `lode doc
// undelete`: the live documents are precisely the ones that command cannot
// act on.
func deletedDocRefAt(n int) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) != n {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return docRefsFiltered(cmd, toComplete, true)
	}
}

// docSetArgs completes `lode doc set <field> <value…> <ref>` on taskSetArgs'
// terms: the field first, then the values and the trailing ref. "reviewers"
// takes actor ids, which have no listing to complete from (see actorRefs), and
// its clear form `lode doc set reviewers <ref>` puts the ref at position 1 —
// so refs are offered from position 1 on, the whole candidate set that
// position admits.
func docSetArgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return staticCompletions(docSetFields, toComplete)
	}
	return docRefsFiltered(cmd, toComplete, false)
}

// projectKeys completes a project argument — the project's id, which is what
// every `<project>`/`<id>` argument on `lode project` takes — carrying the
// project's display name as the description.
//
// It goes through completionClient rather than completionScope: the listing
// is global, and a resolved project is not a precondition for naming one.
func projectKeys(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel, c, _, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()
	resp, _, err := c.ListProjects(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		if strings.HasPrefix(p.ID, toComplete) {
			out = append(out, cobra.Completion(p.ID+"\t"+completionDescription(p.Name)))
		}
	}
	// A project id has no numeric suffix to order by, so plain lexical order
	// is the whole discipline here.
	slices.Sort(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// projectKeyAt completes a project id at argument position n.
func projectKeyAt(n int) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) != n {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return projectKeys(cmd, args, toComplete)
	}
}

// projectSetFocusArgs completes `lode project set focus <concern…> <id>`.
// --clear takes no concerns, so the id is then the only argument there is and
// completes from position 0; without it position 0 holds a concern and the id
// follows. Concerns themselves are not offered: the valid set lives behind
// store.ValidConcern, which the CLI cannot reach.
func projectSetFocusArgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if clear, _ := cmd.Flags().GetBool("clear"); !clear && len(args) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return projectKeys(cmd, args, toComplete)
}

// actorRefs completes an actor argument from the named project's Crew. That
// roster is the only source there is: the API exposes no global actor
// listing, only POST /api/v1/actors. On `lode project crew add` it therefore
// offers the members already on the project, which is the right set for the
// role and lead changes `add` also performs, and harmless when adding
// someone new — the argument is still free text.
func actorRefs(cmd *cobra.Command, project, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel, c, _, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()
	members, _, err := c.ListCrew(ctx, project)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, 0, len(members))
	for _, m := range members {
		if strings.HasPrefix(m.Actor, toComplete) {
			out = append(out, cobra.Completion(m.Actor+"\t"+completionDescription(m.DisplayName)))
		}
	}
	slices.Sort(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// projectThenActor completes the `<project> <actor>` pair both
// `lode project crew add` and `lode project crew remove` take: the project
// id first, then the Crew of the project just named.
func projectThenActor(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return projectKeys(cmd, args, toComplete)
	case 1:
		return actorRefs(cmd, args[0], toComplete)
	default:
		return nil, cobra.ShellCompDirectiveDefault
	}
}

// showRefs completes `lode show [id]`, whose argument is polymorphic.
// classify() (show.go) routes a positional to a task or a document and
// nothing else — a project is reachable only through --project, whose own
// completion is registered separately — so the candidates are the union of
// exactly those two.
//
// The two lookups run concurrently. Each builds its own client and its own
// completionTimeout, so running them in parallel gives both the full budget
// where a sequential pair would spend it on the first and leave the second
// to time out. Each contributes its own candidates or none, so one failing
// still leaves the other's on offer: a partial list beats an empty one.
func showRefs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	lookups := []func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective){taskIDs, docRefs}
	found := make([][]cobra.Completion, len(lookups))
	var wg sync.WaitGroup
	for i, lookup := range lookups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i], _ = lookup(cmd, args, toComplete)
		}()
	}
	wg.Wait()
	return slices.Concat(found...), cobra.ShellCompDirectiveNoFileComp
}
