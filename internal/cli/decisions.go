// decisions.go is the client and the rendering for 025 §10.1's posed
// question: the rows a task carries, addressed as <task>/<key>.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// AddDecision calls POST /api/v1/tasks/{id}/decisions: pose one question.
func (c *Client) AddDecision(ctx context.Context, task string, in model.DecisionInput) (model.Decision, []byte, error) {
	return doJSON[model.Decision](ctx, c, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(task)+"/decisions", in, "decision")
}

// EditDecision calls PATCH /api/v1/tasks/{id}/decisions/{key}: reword,
// regroup or re-parent one unanswered question.
func (c *Client) EditDecision(ctx context.Context, task, key string, in model.DecisionInput) (model.Decision, []byte, error) {
	return doJSON[model.Decision](ctx, c, http.MethodPatch,
		"/api/v1/tasks/"+url.PathEscape(task)+"/decisions/"+url.PathEscape(key), in, "decision")
}

// ParseDecisionRef splits a "<task>/<key>" address into its two halves. A
// string with no "/" is not a decision address, and the error says so rather
// than guessing which half was meant.
func ParseDecisionRef(ref string) (task, key string, err error) {
	task, key, ok := strings.Cut(ref, "/")
	if !ok || task == "" || key == "" {
		return "", "", fmt.Errorf("%q is not a decision: write it as <task>/<key>, e.g. WL-643/x-distribution", ref)
	}
	return task, key, nil
}

// ParseDecisionOption reads one "Label" or "Label:description" offered
// choice. Only the first colon splits, so a description may contain colons.
func ParseDecisionOption(s string) (model.DecisionOption, error) {
	label, desc, _ := strings.Cut(s, ":")
	label, desc = strings.TrimSpace(label), strings.TrimSpace(desc)
	if label == "" {
		return model.DecisionOption{}, fmt.Errorf("option %q has no label: write it as \"Label\" or \"Label:description\"", s)
	}
	return model.DecisionOption{Label: label, Description: desc}, nil
}

// DecisionTable prints the rows of a task in authored order: the address,
// the group, whether it is answered, and the question.
func DecisionTable(w io.Writer, rows []model.Decision) {
	tbl := newTable(
		column{header: "KEY"},
		column{header: "GROUP"},
		column{header: "TYPE"},
		column{header: "ANSWERED"},
		titleColumn("QUESTION"),
	)
	for _, d := range rows {
		answered := "no"
		if d.Answer != nil {
			answered = "yes"
		}
		tbl.add(d.Key, dash(d.Group), d.ResponseType, answered, d.Question)
	}
	tbl.flush(w)
}

// DecisionRender writes one row in full: its address, the question, the
// context, and the answer shape it will take.
func DecisionRender(w io.Writer, d model.Decision) {
	fmt.Fprintf(w, "%s/%s  %s\n", d.Task, d.Key, d.Question)
	tw := newTabwriter(w)
	fmt.Fprintf(tw, "  position:\t%d\n", d.Position)
	if d.Group != "" {
		fmt.Fprintf(tw, "  group:\t%s\n", d.Group)
	}
	fmt.Fprintf(tw, "  type:\t%s\n", d.ResponseType)
	if d.MinPicks != nil || d.MaxPicks != nil {
		fmt.Fprintf(tw, "  picks:\t%s-%s\n", picks(d.MinPicks, "1"), picks(d.MaxPicks, strconv.Itoa(len(d.Options))))
	}
	for _, o := range d.Options {
		if o.Description != "" {
			fmt.Fprintf(tw, "  option:\t%s — %s\n", o.Label, o.Description)
			continue
		}
		fmt.Fprintf(tw, "  option:\t%s\n", o.Label)
	}
	if d.DecidedAt != nil {
		fmt.Fprintf(tw, "  decided:\t%s by %s\n", LocalTime(*d.DecidedAt), dash(d.DecidedBy))
	}
	tw.Flush()
	if d.Context != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(d.Context, "\n"))
	}
}

// picks renders one bound of a multi_select's range, falling back to the
// implicit default when the row leaves it unset.
func picks(n *int, orDefault string) string {
	if n == nil {
		return orDefault
	}
	return strconv.Itoa(*n)
}
