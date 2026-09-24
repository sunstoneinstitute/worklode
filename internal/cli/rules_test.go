package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestRuleRender(t *testing.T) {
	c := model.Rule{
		Ref: "WL-RULE-12", Status: "accepted", Version: 3, Heading: "Lease lifecycle",
		Body:       "\nA lease is renewed every minute.\n",
		ArrangedIn: []model.RuleArrangement{{DocRef: "WL-SPEC-4", Anchor: "sec-2", Depth: 2, RuleVersion: 3}},
		UpdatedAt:  time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
	}
	var b bytes.Buffer
	RuleRender(&b, c)
	out := b.String()
	for _, want := range []string{"WL-RULE-12", "Lease lifecycle", "accepted", "version:  3", "WL-SPEC-4#sec-2", "renewed every minute"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}

func TestRuleRenderGovernedTasks(t *testing.T) {
	var b strings.Builder
	RuleRender(&b, model.Rule{Ref: "WL-RULE-2", Heading: "Sub", Status: "accepted", Version: 2,
		GovernedTasks: []model.RuleTask{{ID: "WL-7", Title: "Do it", State: "ready", Source: "plan", RuleVersion: 1}}})
	if !strings.Contains(b.String(), "  governs:  WL-7 Do it (ready, plan, v1)\n") {
		t.Errorf("render:\n%s", b.String())
	}
}

func TestRuleRenderEdges(t *testing.T) {
	var b strings.Builder
	RuleRender(&b, model.Rule{Ref: "WL-RULE-2", Heading: "Sub", Status: "accepted", Version: 1,
		Edges: []model.RuleEdge{
			{Type: "constrains", From: "WL-RULE-2", To: "WL-RULE-3", ToHeading: "Other", Source: "manual"},
			{Type: "references", From: "WL-RULE-9", FromHeading: "Cites it", To: "WL-RULE-2", Source: "derived"},
		}})
	out := b.String()
	for _, want := range []string{
		"  constrains: WL-RULE-3 Other (manual)\n",
		"  references: WL-RULE-9 Cites it (derived, incoming)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}

func TestRuleRenderOwnerAndTags(t *testing.T) {
	var b strings.Builder
	RuleRender(&b, model.Rule{Ref: "WL-RULE-2", Heading: "Sub", Status: "accepted", Version: 1,
		Owner: "stig", Tags: []string{"storage", "search"}})
	out := b.String()
	for _, want := range []string{"  owner:    stig\n", "  tags:     storage, search\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	RuleRender(&b, model.Rule{Ref: "WL-RULE-3", Heading: "Sub", Status: "accepted", Version: 1})
	if out := b.String(); strings.Contains(out, "owner:") || strings.Contains(out, "tags:") {
		t.Errorf("render should omit unset owner/tags:\n%s", out)
	}
}

func TestRuleVersionsTable(t *testing.T) {
	var b strings.Builder
	RuleVersionsTable(&b, []model.RuleVersion{
		{Version: 2, Heading: "Sub", CreatedAt: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)},
		{Version: 1, Heading: "Sub", CreatedAt: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)},
	})
	out := b.String()
	if !strings.HasPrefix(out, "VERSION") || !strings.Contains(out, "2") || !strings.Contains(out, "Sub") {
		t.Errorf("table:\n%s", out)
	}
}

func TestRulesTable(t *testing.T) {
	var b bytes.Buffer
	RulesTable(&b, []model.Rule{{
		Ref: "WL-RULE-12", Status: "draft", Version: 2, Heading: "How to read this set",
		ArrangedIn: []model.RuleArrangement{{DocRef: "WL-SPEC-73", Anchor: "sec-1"}},
	}})
	out := b.String()
	for _, want := range []string{"REF", "WL-RULE-12", "draft", "2", "WL-SPEC-73#sec-1", "How to read this set"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}
