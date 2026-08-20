package cmd

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// findCommand walks the cobra tree by name, e.g. ("task", "delete").
func findCommand(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cur := rootCmd
	for _, name := range path {
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			t.Fatalf("lode %s: no %q subcommand", strings.Join(path, " "), name)
		}
		cur = next
	}
	return cur
}

// TestDeleteCommandsRegistered pins the 044 §5 CLI surface: delete and
// undelete on both entity types, --justification spelled the same on both
// deletes and absent from both undeletes, and no -j shorthand — the flag is
// meant to cost a moment's typing.
func TestDeleteCommandsRegistered(t *testing.T) {
	for _, entity := range []string{"task", "doc"} {
		del := findCommand(t, entity, "delete")
		f := del.Flags().Lookup("justification")
		if f == nil {
			t.Fatalf("lode %s delete has no --justification flag", entity)
		}
		if f.Shorthand != "" {
			t.Fatalf("lode %s delete --justification has shorthand -%s; 044 §5 takes no shorthand",
				entity, f.Shorthand)
		}
		und := findCommand(t, entity, "undelete")
		if und.Flags().Lookup("justification") != nil {
			t.Fatalf("lode %s undelete has a --justification flag; undelete needs none (044 §3)", entity)
		}
	}
}

// TestTaskDeleteHelpPointsAtAbandon pins that `lode task delete`'s help sends
// the reader to abandon first (044 §1): abandon keeps the decision record,
// delete is for a row that should not have existed.
func TestTaskDeleteHelpPointsAtAbandon(t *testing.T) {
	help := findCommand(t, "task", "delete").Long
	if !strings.Contains(help, "abandon") {
		t.Fatalf("lode task delete help does not mention abandon:\n%s", help)
	}
}

// TestDeleteCommandsRejectMissingArg pins that each of the four commands needs
// its id argument; cobra rejects the call before any server round trip.
func TestDeleteCommandsRejectMissingArg(t *testing.T) {
	for _, args := range [][]string{
		{"task", "delete"},
		{"task", "undelete"},
		{"doc", "delete"},
		{"doc", "undelete"},
	} {
		out, err := runLode(t, args...)
		if err == nil {
			t.Fatalf("lode %s with no id succeeded\noutput: %s", strings.Join(args, " "), out)
		}
		if !strings.Contains(err.Error(), "accepts 1 arg") {
			t.Fatalf("lode %s with no id: err = %v, want an argument-count error",
				strings.Join(args, " "), err)
		}
	}
}

// TestListsHaveDeletedFlag pins `--deleted` on both list commands and says
// what it is: a switch to the tombstoned rows, not an addition to the live
// ones (044 §5).
func TestListsHaveDeletedFlag(t *testing.T) {
	for _, entity := range []string{"task", "doc"} {
		f := findCommand(t, entity, "list").Flags().Lookup("deleted")
		if f == nil {
			t.Fatalf("lode %s list has no --deleted flag", entity)
		}
		if !strings.Contains(f.Usage, "instead of live") {
			t.Fatalf("lode %s list --deleted usage does not say it replaces the live list: %q",
				entity, f.Usage)
		}
	}
}

// TestTaskDeleteListUndeleteRoundTrip drives the whole task half through the
// CLI against a live server: an abandoned task is deleted, leaves the default
// list, shows up under --deleted despite the default status filter that would
// otherwise hide an abandoned row, and comes back on undelete.
func TestTaskDeleteListUndeleteRoundTrip(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	kept := createTestTask(t, c, "Kept")
	noise := createTestTask(t, c, "Seeded by mistake")

	// Abandoned, so the default --status filter would hide it: --deleted has
	// to ignore that filter to list the tombstone at all (044 §1).
	if _, err := runLode(t, "task", "abandon", noise.ID); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	out, err := runLode(t, "task", "delete", noise.ID, "--justification", "seeded by mistake")
	if err != nil {
		t.Fatalf("task delete: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, noise.ID) || !strings.Contains(out, "seeded by mistake") {
		t.Fatalf("task delete output = %q, want the id and the reason", out)
	}

	if got := taskListIDs(t, "--project", "proj"); !reflect.DeepEqual(got, []string{kept.ID}) {
		t.Fatalf("live task list = %v, want [%s]", got, kept.ID)
	}
	if got := taskListIDs(t, "--project", "proj", "--deleted"); !reflect.DeepEqual(got, []string{noise.ID}) {
		t.Fatalf("deleted task list = %v, want [%s]", got, noise.ID)
	}
	// An explicit --status still narrows within the tombstoned set.
	if got := taskListIDs(t, "--project", "proj", "--deleted", "--status", "ready"); len(got) != 0 {
		t.Fatalf("deleted+ready task list = %v, want empty (the tombstone is abandoned)", got)
	}

	if out, err := runLode(t, "task", "undelete", noise.ID); err != nil {
		t.Fatalf("task undelete: %v\noutput: %s", err, out)
	}
	if got := taskListIDs(t, "--project", "proj", "--deleted"); len(got) != 0 {
		t.Fatalf("deleted task list after undelete = %v, want empty", got)
	}
}

// TestDocDeleteListUndeleteRoundTrip is the document half: delete switches the
// list, undelete switches it back.
func TestDocDeleteListUndeleteRoundTrip(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	doc, _, err := c.CreateDoc(context.Background(), model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 1, Slug: "deletable-spec", Body: docTestBody,
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	out, err := runLode(t, "doc", "delete", strconv.FormatInt(doc.ID, 10),
		"--justification", "wrong corpus number")
	if err != nil {
		t.Fatalf("doc delete: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "wrong corpus number") {
		t.Fatalf("doc delete output = %q, want the reason", out)
	}

	if ids := docListIDs(t); len(ids) != 0 {
		t.Fatalf("live doc list = %v, want empty", ids)
	}
	if ids := docListIDs(t, "--deleted"); !reflect.DeepEqual(ids, []int64{doc.ID}) {
		t.Fatalf("deleted doc list = %v, want [%d]", ids, doc.ID)
	}

	if out, err := runLode(t, "doc", "undelete", strconv.FormatInt(doc.ID, 10)); err != nil {
		t.Fatalf("doc undelete: %v\noutput: %s", err, out)
	}
	if ids := docListIDs(t); !reflect.DeepEqual(ids, []int64{doc.ID}) {
		t.Fatalf("live doc list after undelete = %v, want [%d]", ids, doc.ID)
	}
}

// docListIDs runs `lode doc list --json` with extra args and returns the ids.
func docListIDs(t *testing.T, args ...string) []int64 {
	t.Helper()
	out, err := runLode(t, append([]string{"doc", "list", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("lode doc list: %v\noutput: %s", err, out)
	}
	var resp struct {
		Docs []model.Doc `json:"docs"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	ids := make([]int64, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		ids = append(ids, d.ID)
	}
	return ids
}

// TestResolveDeletedStatusFilter pins the --deleted/--status interaction. A
// tombstone is orthogonal to state (044 §1), so the default open-state filter
// would hide most tombstones: --deleted alone drops the state filter, while an
// explicit --status still narrows within the tombstoned set.
func TestResolveDeletedStatusFilter(t *testing.T) {
	open := []string{"draft", "ready", "in_progress", "in_review"}
	cases := []struct {
		name        string
		statuses    []string
		deleted     bool
		statusGiven bool
		want        []string
	}{
		{"no flags keeps the open-state default", nil, false, false, open},
		{"--deleted alone lists every state", nil, true, false, nil},
		{"--deleted with an explicit --status narrows", []string{"abandoned"}, true, true, []string{"abandoned"}},
		{"--deleted --status all lists every state", []string{"all"}, true, true, nil},
		{"--status without --deleted is unchanged", []string{"merged"}, false, true, []string{"merged"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveDeletedStatusFilter(c.statuses, c.deleted, c.statusGiven)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("resolveDeletedStatusFilter(%v, %v, %v) = %v, want %v",
					c.statuses, c.deleted, c.statusGiven, got, c.want)
			}
		})
	}
}
