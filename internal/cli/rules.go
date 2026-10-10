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

// ListRules calls GET /api/v1/rules: rules without their text, in
// arrangement order when Doc is set, by number otherwise.
func (c *Client) ListRules(ctx context.Context, p model.RuleListParams) ([]model.Rule, []byte, error) {
	return doJSON[[]model.Rule](ctx, c, http.MethodGet, withParams("/api/v1/rules", p), nil, "rules")
}

// RulesTable prints `lode rule list`: one row per rule with every
// placement that arranges it.
func RulesTable(w io.Writer, rules []model.Rule) {
	tbl := newTable(
		column{header: "REF"},
		column{header: "KIND"},
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
		tbl.add(c.Ref, c.Kind, c.Status, strconv.Itoa(c.Version), strings.Join(placed, ", "), c.Heading)
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
	fmt.Fprintf(w, "  kind:     %s\n", c.Kind)
	if c.ConceptIRI != "" {
		fmt.Fprintf(w, "  concept:  %s\n", c.ConceptIRI)
	}
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
		fmt.Fprintf(w, "  covered:  %s (%s)\n", p.DocRef, p.Status)
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

// AddRule calls POST /api/v1/rules: a standalone rule (WL-SPEC-77 §19.2).
func (c *Client) AddRule(ctx context.Context, in model.AddRuleInput) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodPost, "/api/v1/rules", in, "rule")
}

// AcceptRule calls POST /api/v1/rules/{ref}/accept.
func (c *Client) AcceptRule(ctx context.Context, ref string, in model.AcceptRuleInput) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodPost, "/api/v1/rules/"+url.PathEscape(ref)+"/accept", in, "rule")
}

// ArrangeRule calls POST /api/v1/docs/{id}/rules: place a rule in a spec
// (WL-SPEC-77 §19.3).
func (c *Client) ArrangeRule(ctx context.Context, docID int64, in model.ArrangeRuleInput) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodPost, docPath(docID, "/rules"), in, "rule")
}

// UnarrangeRule calls DELETE /api/v1/docs/{id}/rules/{ref}.
func (c *Client) UnarrangeRule(ctx context.Context, docID int64, ref string) (model.Rule, []byte, error) {
	return doJSON[model.Rule](ctx, c, http.MethodDelete, docPath(docID, "/rules/"+url.PathEscape(ref)), nil, "rule")
}

// SetRuleMeta calls PATCH /api/v1/rules/{ref}: owner and/or tags.
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

// GetRuleClosure calls GET /api/v1/rules/{ref}/closure.
func (c *Client) GetRuleClosure(ctx context.Context, ref string) (model.RuleClosure, []byte, error) {
	return doJSON[model.RuleClosure](ctx, c, http.MethodGet, "/api/v1/rules/"+url.PathEscape(ref)+"/closure", nil, "rule closure")
}

// RuleClosureTable prints `lode show <rule> --closure`: one row per closure
// member with the edge that first reached it, then the total word count.
func RuleClosureTable(w io.Writer, c model.RuleClosure) {
	tbl := newTable(
		column{header: "REF"},
		column{header: "KIND"},
		column{header: "WORDS"},
		column{header: "VIA"},
		titleColumn("HEADING"),
	)
	for _, m := range c.Members {
		via := "-"
		if m.Edge != "" {
			via = m.Edge + " " + m.From
		}
		tbl.add(m.Ref, m.Kind, strconv.Itoa(m.Words), via, m.Heading)
	}
	tbl.flush(w)
	fmt.Fprintf(w, "\n%d rules, %d words\n", len(c.Members), c.Words)
}

// RuleLint calls GET /api/v1/projects/{id}/rules/lint.
func (c *Client) RuleLint(ctx context.Context, project string) (model.RuleLint, []byte, error) {
	return doJSON[model.RuleLint](ctx, c, http.MethodGet, "/api/v1/projects/"+url.PathEscape(project)+"/rules/lint", nil, "rule lint")
}

// RuleLintRender prints `lode rule lint`: the corpus measures, rules per
// spec, then one row per finding naming its rule.
func RuleLintRender(w io.Writer, l model.RuleLint) {
	fmt.Fprintf(w, "%d rules. Closure rules: median %d, p90 %d. Closure words: median %d, p90 %d.\n\n",
		l.Rules, l.ClosureRules.Median, l.ClosureRules.P90, l.ClosureWords.Median, l.ClosureWords.P90)
	specs := newTable(column{header: "SPEC"}, column{header: "RULES"})
	for _, sp := range l.Specs {
		specs.add(sp.Spec, strconv.Itoa(sp.Rules))
	}
	specs.flush(w)
	if len(l.Findings) == 0 {
		return
	}
	fmt.Fprintln(w)
	tbl := newTable(column{header: "RULE"}, column{header: "CHECK"}, titleColumn("DETAIL"))
	for _, f := range l.Findings {
		tbl.add(f.Rule, f.Check, f.Detail)
	}
	tbl.flush(w)
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
