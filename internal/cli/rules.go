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

// GetRule calls GET /api/v1/rules/{ref}: one design rule with its
// current text and the documents arranging it.
func (c *Client) GetRule(ctx context.Context, ref string) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodGet, "/api/v1/rules/"+url.PathEscape(ref), nil, "rule")
}

// RuleListFilter narrows ListRules. Doc is any document ref
// (WL-SPEC-73, a slug, an id). Zero-valued fields do not filter.
type RuleListFilter struct {
	Project, Doc, Status string
}

// ListRules calls GET /api/v1/rules: rules without their text, in
// arrangement order when Doc is set, by number otherwise.
func (c *Client) ListRules(ctx context.Context, f RuleListFilter) ([]model.Rule, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Doc != "" {
		q.Set("doc", f.Doc)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	path := "/api/v1/rules"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return doJSON[[]model.Rule](ctx, c, http.MethodGet, path, nil, "rules")
}

// RulesTable prints `lode rule list`: one row per rule with every
// placement that arranges it.
func RulesTable(w io.Writer, rules []model.Rule) {
	tbl := newTable(
		column{header: "REF"},
		column{header: "STATUS"},
		column{header: "VER"},
		column{header: "ARRANGED"},
		titleColumn("HEADING"),
	)
	for _, c := range rules {
		placed := make([]string, len(c.ArrangedIn))
		for i, a := range c.ArrangedIn {
			placed[i] = a.DocRef + "#" + a.Anchor
		}
		tbl.add(c.Ref, c.Status, strconv.Itoa(c.Version), strings.Join(placed, ", "), c.Heading)
	}
	tbl.flush(w)
}

// ruleEdgeInverse names the inverse a reader sees on the far end of a stored
// edge that has one (WL-SPEC-77 §4). The inverse is never stored.
var ruleEdgeInverse = map[string]string{"amends": "amendedBy", "supersedes": "supersededBy"}

// RuleRender is the human view of one rule: its ref and heading, status
// and version, where it is arranged, then its text.
func RuleRender(w io.Writer, c model.Rule) {
	fmt.Fprintf(w, "%s  %s\n", c.Ref, c.Heading)
	fmt.Fprintf(w, "  status:   %s\n", c.Status)
	fmt.Fprintf(w, "  version:  %d\n", c.Version)
	if c.Owner != "" {
		fmt.Fprintf(w, "  owner:    %s\n", c.Owner)
	}
	if len(c.Tags) > 0 {
		fmt.Fprintf(w, "  tags:     %s\n", strings.Join(c.Tags, ", "))
	}
	fmt.Fprintf(w, "  updated:  %s\n", LocalTime(c.UpdatedAt))
	for _, a := range c.ArrangedIn {
		fmt.Fprintf(w, "  arranged: %s#%s (depth %d, v%d)\n", a.DocRef, a.Anchor, a.Depth, a.RuleVersion)
	}
	for _, p := range c.CoveredBy {
		fmt.Fprintf(w, "  covered:  %s (%s, %s)\n", p.DocRef, p.Status, p.Coverage)
	}
	for _, gt := range c.GovernedTasks {
		fmt.Fprintf(w, "  governs:  %s %s (%s, %s, v%d)\n", gt.ID, gt.Title, gt.State, gt.Source, gt.RuleVersion)
	}
	for _, e := range c.Edges {
		switch {
		case e.From == c.Ref:
			fmt.Fprintf(w, "  %-9s %s %s (%s)\n", e.Type+":", e.To, e.ToHeading, e.Source)
		case ruleEdgeInverse[e.Type] != "":
			fmt.Fprintf(w, "  %-9s %s %s (%s)\n", ruleEdgeInverse[e.Type]+":", e.From, e.FromHeading, e.Source)
		default:
			fmt.Fprintf(w, "  %-9s %s %s (%s, incoming)\n", e.Type+":", e.From, e.FromHeading, e.Source)
		}
	}
	if c.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, c.Body)
	}
}

// RuleAmendmentsRender prints a rule's folded amendments beneath its text:
// pending ones as references, in-force ones as attributed blocks.
func RuleAmendmentsRender(w io.Writer, blocks, pending []string) {
	var b strings.Builder
	for _, p := range pending {
		fmt.Fprintf(&b, "\n> Pending %s (not yet effective)\n", p)
	}
	for _, block := range blocks {
		b.WriteString("\n" + block + "\n")
	}
	if b.Len() > 0 {
		Markdown(w, b.String())
	}
}

// EditRule calls PUT /api/v1/rules/{ref}.
func (c *Client) EditRule(ctx context.Context, ref string, in model.EditRuleInput) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodPut, "/api/v1/rules/"+url.PathEscape(ref), in, "rule")
}

// SetRuleMeta calls PATCH /api/v1/rules/{ref}: owner and/or tags (S15).
func (c *Client) SetRuleMeta(ctx context.Context, ref string, in model.RuleMetaInput) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodPatch, "/api/v1/rules/"+url.PathEscape(ref), in, "rule")
}

// ListRuleVersions calls GET /api/v1/rules/{ref}/versions.
func (c *Client) ListRuleVersions(ctx context.Context, ref string) ([]model.RuleVersion, []byte, error) {
	return doJSON[[]model.RuleVersion](ctx, c, http.MethodGet, "/api/v1/rules/"+url.PathEscape(ref)+"/versions", nil, "rule versions")
}

// GetRuleVersion calls GET /api/v1/rules/{ref}/versions/{n}.
func (c *Client) GetRuleVersion(ctx context.Context, ref string, version int) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/rules/%s/versions/%d", url.PathEscape(ref), version), nil, "rule")
}

// RuleVersionsTable lists a rule's versions, newest first: the `lode
// rule versions` view.
func RuleVersionsTable(w io.Writer, vs []model.RuleVersion) {
	tbl := newTable(
		column{header: "VERSION"},
		titleColumn("HEADING"),
		column{header: "CREATED"},
	)
	for _, v := range vs {
		tbl.add(strconv.Itoa(v.Version), v.Heading, LocalTime(v.CreatedAt))
	}
	tbl.flush(w)
}
