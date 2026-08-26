package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newDocTodoCmd() *cobra.Command {
	var deps bool
	cmd := &cobra.Command{
		Use:   "todo <ref>",
		Short: "What is left before a spec is fully implemented",
		Long: `Join a spec's planning gap, its unexecuted plans, and the ordering
between them into one work list (026 §2.5).

<ref> is any reference §4 resolves: a filename, a repo-relative path, or
the WL-SPEC-25 shorthand. Every item is typed by the act that discharges
it — writing a plan (unplanned, partial), a human accepting one
(plan-draft), executing one (unexecuted), or landing the plan that holds
it up (blocked).

The list is an execution queue, not a set: items are ordered
topologically over the plans' requires edges. The exit status is 0
whether or not work remains — this is a report. A ref that names no
document exits nonzero, so a typo cannot read as "nothing outstanding".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocTodo(cmd, args[0], deps)
		},
	}
	cmd.Flags().BoolVar(&deps, "deps", false,
		"follow requires edges transitively, so the answer covers the specs this one depends on")
	return cmd
}

// docTodoItem is one item of `lode doc todo --json`. Per ADR 036 this is an
// internal/cmd stdout contract, not an HTTP body, so it is declared here —
// and named, so the guard in internal/model/rule_test.go can see it.
//
// anchor and anchors are disjoint: a plan-level item names the one section it
// is attributed to, a collapsed planning gap names every section it covers,
// and the document-level acceptance item names neither.
type docTodoItem struct {
	Type    string   `json:"type"`
	Doc     string   `json:"doc"`
	Anchor  string   `json:"anchor,omitempty"`
	Anchors []string `json:"anchors,omitempty"`
	Heading string   `json:"heading"`
	Plan    string   `json:"plan,omitempty"`
	Task    string   `json:"task,omitempty"`
	Detail  string   `json:"detail"`
}

// docTodoDiagnostics is the footer as JSON: what the walk did not do, and why
// the answer may be narrower than the question.
type docTodoDiagnostics struct {
	Unfollowed []string `json:"unfollowed"`
	Cycles     []string `json:"cycles"`
	Notes      []string `json:"notes"`
}

// docTodoResult is the whole --json document: both halves as sibling keys, so
// a consumer reads the work list and the caveats on it from one parse.
type docTodoResult struct {
	Items       []docTodoItem      `json:"items"`
	Diagnostics docTodoDiagnostics `json:"diagnostics"`
}

func runDocTodo(cmd *cobra.Command, ref string, deps bool) error {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	resp, _, err := c.ListDocs(cmd.Context(), cli.DocListFilter{})
	if err != nil {
		return err
	}
	// Same resolution as `lode show`, tiers 1 and 2 both: a shorthand whose key
	// is not this checkout's — which is every shorthand in a repo with no
	// project_key — is the backbone's to answer, not a miss (026 §4.2). A tier-3
	// key nothing carries is returned as an error rather than printed: the exit
	// status of this command means "work remains" for a document it resolved,
	// so a ref it could not resolve must not read as "no work".
	target, _, err := resolveDocRefTiers(cmd.Context(), c, resp.Docs, cfg.ProjectKey, ref)
	if err != nil {
		return err
	}
	docs, err := docTodoCorpus(cmd.Context(), c, resp.Docs)
	if err != nil {
		return err
	}

	closed, err := docTodoClosure(cmd, c, cfg)
	if err != nil {
		return err
	}
	items, diag, err := designdoc.Todo(docs, designdoc.CorpusPath(target.Kind, target.Slug),
		designdoc.TodoOptions{Deps: deps, Closed: closed})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return writeDocTodoJSON(cmd, items, diag)
	}
	writeDocTodoTable(cmd.OutOrStdout(), designdoc.CorpusPath(target.Kind, target.Slug), items, diag)
	return nil
}

// docTodoCorpusConcurrency bounds the body fetches below. The walk needs every
// document's frontmatter, and only GET /docs/{id} carries a body, so the cost
// is one request per document; a small fan-out turns a corpus-sized serial
// round trip into roughly one, without opening a connection per document.
const docTodoCorpusConcurrency = 8

// docTodoCorpus loads every document the backbone serves as a CorpusDoc, so
// the walk of 026 §2.5 reads the same corpus `lode doc list` does.
//
// The walk is a pure function over parsed documents, and the facts it needs —
// a plan's covers levels, its requires, its task — live in frontmatter, which
// only the body carries. Each document is therefore fetched and re-parsed
// rather than read from the backbone's own section and edge rows: those rows
// are the server's index of the same frontmatter, and reading the source keeps
// one parser rather than two readings that can disagree.
func docTodoCorpus(ctx context.Context, c *cli.Client, docs []model.Doc) ([]designdoc.CorpusDoc, error) {
	out := make([]designdoc.CorpusDoc, len(docs))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(docTodoCorpusConcurrency)
	for i, d := range docs {
		g.Go(func() error {
			detail, _, err := c.GetDoc(ctx, d.ID)
			if err != nil {
				return fmt.Errorf("read document %s: %w", d.Slug, err)
			}
			cd, err := designdoc.CorpusDocFromBody(
				designdoc.CorpusPath(d.Kind, d.Slug), d.Kind, []byte(detail.Doc.Body))
			if err != nil {
				return err
			}
			out[i] = cd
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// docTodoClosure builds the task-closure lookup from one task list: closure is
// the server's answer, never a state string (026 §2.5), so the whole project's
// tasks are fetched once and indexed rather than asked for one at a time. A
// task the response does not carry is unknown, which is never evidence of
// closure.
func docTodoClosure(cmd *cobra.Command, c *cli.Client, cfg cli.Config) (func(string) (bool, bool), error) {
	// No working directory: the git-remote fallback would cost a subprocess
	// to narrow a list this command wants wide anyway. Unscoped returns every
	// project's tasks, which resolves strictly more of the plans' task ids.
	scope := cli.ResolveScope(cmd.Context(), c, cfg, "")
	resp, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{Project: scope.Project})
	if err != nil {
		return nil, err
	}
	closed := make(map[string]bool, len(resp.Tasks))
	for _, t := range resp.Tasks {
		closed[t.ID] = t.Closed
	}
	return func(taskID string) (bool, bool) {
		c, known := closed[taskID]
		return c, known
	}, nil
}

func writeDocTodoJSON(cmd *cobra.Command, items []designdoc.TodoItem, diag designdoc.Diagnostics) error {
	res := docTodoResult{
		Items: make([]docTodoItem, 0, len(items)),
		Diagnostics: docTodoDiagnostics{
			Unfollowed: orEmpty(diag.Unfollowed),
			Cycles:     orEmpty(diag.Cycles),
			Notes:      orEmpty(diag.Notes),
		},
	}
	for _, it := range items {
		res.Items = append(res.Items, docTodoItem{
			Type: it.Type, Doc: it.Doc, Anchor: it.Anchor, Anchors: it.Anchors,
			Heading: it.Heading, Plan: it.Plan, Task: it.Task, Detail: it.Detail,
		})
	}
	b, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	printRaw(cmd, b)
	return nil
}

// orEmpty keeps a diagnostics key an empty array rather than null, so a
// consumer can range over it without a nil check.
func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// A collapsed item's anchor list is printed whole up to anchorFull
// characters, and otherwise truncated at anchorBudget with its total count. A
// document's gap can name fifty-odd sections; the count is the informative
// part, the full list would wrap every line.
const (
	anchorFull   = 30
	anchorBudget = 22
)

// docTodoSections renders an item's section column: the one anchor a
// plan-level item is attributed to, the (possibly truncated) list a collapsed
// gap names, or "(document)" for the acceptance item, which is about the
// document itself and names no section.
func docTodoSections(it designdoc.TodoItem) string {
	if it.Anchor != "" {
		return it.Anchor
	}
	if len(it.Anchors) == 0 {
		return "(document)"
	}
	if full := strings.Join(it.Anchors, ","); runeLen(full) <= anchorFull {
		return full
	}
	var b strings.Builder
	spent := 0 // runes written, the same unit anchorBudget is stated in
	for i, a := range it.Anchors {
		if i > 0 {
			if spent+1+runeLen(a) > anchorBudget {
				fmt.Fprintf(&b, ",…(%d)", len(it.Anchors))
				return b.String()
			}
			b.WriteByte(',')
			spent++
		}
		b.WriteString(a)
		spent += runeLen(a)
	}
	return b.String()
}

// docTodoRow is one item flattened into its printed cells. reason is the
// second line a blocked item gets (see writeDocTodoRun); it is empty on every
// other type.
type docTodoRow struct{ doc, typ, sections, plan, detail, reason string }

// docTodoLegend says what act each type asks for. It is printed once, because
// the alternative is printing it on every row: a plan-draft item's own reason
// ("plan is draft: accepting it is a human act") only restates its type, and
// the detail column is better spent on the section's heading — which the
// --json consumer could already read and the human reader could not.
const docTodoLegend = "types: unplanned, partial — write a plan;   plan-draft — a human accepts it\n" +
	"       unexecuted — execute the plan;       blocked — land the plan it waits on"

// docTodoContinuedNote explains a heading the queue comes back to. The repeat
// is not a duplicate: those items rank later because they wait on plans listed
// above them.
const docTodoContinuedNote = "(continued) marks a document the queue returns to: those items wait on plans above"

// writeDocTodoTable prints the work list one item per line, under a heading
// naming the document each run of items belongs to. The heading repeats when
// the document changes, because the order is the execution queue (026 §2.5)
// and gathering each document's items together would destroy it.
//
// Column widths are measured per run, not over the whole list: one document
// with a long plan filename would otherwise pad every planless row in every
// other document out to its width, which on this corpus is most of them.
func writeDocTodoTable(w io.Writer, docPath string, items []designdoc.TodoItem, diag designdoc.Diagnostics) {
	if len(items) == 0 {
		fmt.Fprintf(w, "nothing outstanding: every section of %s is planned and executed\n", docPath)
		writeDocTodoFooter(w, diag)
		return
	}

	rows := make([]docTodoRow, 0, len(items))
	for _, it := range items {
		rows = append(rows, docTodoRowFor(it))
	}
	seen := map[string]bool{}
	continued := false
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].doc == rows[start].doc {
			end++
		}
		if start > 0 {
			fmt.Fprintln(w)
		}
		doc := rows[start].doc
		if seen[doc] {
			continued = true
			fmt.Fprintf(w, "%s (continued)\n", doc)
		} else {
			fmt.Fprintln(w, doc)
		}
		seen[doc] = true
		writeDocTodoRun(w, rows[start:end])
		start = end
	}
	fmt.Fprintf(w, "\n%s across %s\n", plural(len(items), "item"), plural(len(seen), "document"))
	if continued {
		fmt.Fprintln(w, docTodoContinuedNote)
	}
	fmt.Fprintf(w, "\n%s\n", docTodoLegend)
	writeDocTodoFooter(w, diag)
}

// docTodoRowFor flattens one item into its cells, and decides which of the
// item's two strings the detail column is worth spending on:
//
//   - plan-draft: the section's Heading. The item's own reason says only that
//     the plan or document is a draft, which the type column already said.
//   - blocked: nothing on the first line. Its reason names a second
//     sixty-character plan filename, which no arrangement fits beside the
//     first, so it goes on a continuation line instead of running off the edge.
//   - everything else: the reason, which carries the task id or the section
//     count that nothing else on the row does.
func docTodoRowFor(it designdoc.TodoItem) docTodoRow {
	r := docTodoRow{
		doc: it.Doc, typ: it.Type, sections: docTodoSections(it),
		plan: shortenPlanRefs(it.Plan),
	}
	detail := shortenPlanRefs(it.Detail)
	switch {
	case it.Type == designdoc.TodoPlanDraft && it.Heading != "":
		r.detail = ellipsize(it.Heading, maxHeadingCell)
	case it.Type == designdoc.TodoBlocked:
		r.reason = detail
	default:
		r.detail = detail
	}
	return r
}

// shortenPlanRefs drops the plan corpus's own directory from every plan
// reference in s — the column and the "requires ..." reasons alike, so one
// rule covers both. What is left is the plan's canonical reference, its bare
// filename (docs/authoring-design-docs.md); the directory it shares with every
// other plan is the one part a reader gains nothing from. The --json items
// keep the full repo-relative paths.
func shortenPlanRefs(s string) string {
	return strings.ReplaceAll(s, designdoc.CorpusDir("plan")+"/", "")
}

// writeDocTodoRun prints one document's consecutive items, aligned among
// themselves. A row naming no plan drops that column rather than printing a
// run's width of placeholder space in front of its detail — the leading
// planning-gap rows would otherwise start past column 100. An item carrying a
// reason gets a second line, indented to the plan column so it reads as that
// item's tail and nothing else on the row has to move.
func writeDocTodoRun(w io.Writer, rows []docTodoRow) {
	typeW, sectionW, planW := 0, 0, 0
	for _, r := range rows {
		typeW = max(typeW, runeLen(r.typ))
		sectionW = max(sectionW, runeLen(r.sections))
		planW = max(planW, runeLen(r.plan))
	}
	indent := 2 + typeW + 2 + sectionW + 2
	for _, r := range rows {
		line := fmt.Sprintf("  %-*s  %-*s", typeW, r.typ, sectionW, r.sections)
		switch {
		case r.plan == "":
			// Nothing to align against: a row with no plan puts its detail
			// straight after the section column rather than across the width
			// of the run's longest plan filename.
			line += "  " + r.detail
		case r.reason != "":
			// The reason takes the next line, so the plan ends this one and
			// needs no padding.
			line += "  " + r.plan
		default:
			line += fmt.Sprintf("  %-*s  %s", planW, r.plan, r.detail)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if r.reason != "" {
			fmt.Fprintf(w, "%*s%s\n", indent, "", r.reason)
		}
	}
}

func runeLen(s string) int { return len([]rune(s)) }

// maxHeadingCell bounds the detail column when it carries a heading. A
// document's own title runs past eighty characters here; it is prose, so the
// tail is the part a reader can spare. A produced reason is never cut — it
// states a fact, and half a fact is worse than a wide line.
const maxHeadingCell = 72

func ellipsize(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return strings.TrimRight(string(r[:width-1]), " ") + "…"
}

// writeDocTodoFooter prints the diagnostics: one labelled block per non-empty
// category, its count in the label so a long list reads at a glance.
func writeDocTodoFooter(w io.Writer, diag designdoc.Diagnostics) {
	blocks := []struct {
		label string
		lines []string
	}{
		{"unfollowed requires edges", diag.Unfollowed},
		{"requires cycles", diag.Cycles},
		{"notes", diag.Notes},
	}
	for _, b := range blocks {
		if len(b.lines) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s (%d):\n", b.label, len(b.lines))
		for _, l := range b.lines {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
}

// plural renders a count with its noun, "s" added past one.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
