package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRenderSpans: renderSpans renders what renderBody does, and its spans
// tile the rendered text with each rule span equal to the rule body bytes it
// names and each template span equal to the docs.body bytes it names
// (WL-REQ-1317).
func TestRenderSpans(t *testing.T) {
	cases := map[string]struct {
		body  string
		texts []arrangedText
	}{
		"replaced copy": {
			"Intro.\n\n## 1. Head {#sec-1}\n\n### 1.1 Old {#sec-1.1}\n\nOld text.\n\n## Sources\n\nA list.\n",
			[]arrangedText{
				{heading: "Renamed head", depth: 2},
				{heading: "New", body: "\nNew text.\n", rule: true, ref: "P1-REQ-4", depth: 3, ruleID: 4, ruleVersion: 2},
			},
		},
		"agreeing copy": {
			"Intro.\n\n## 1. Renamed head {#sec-1}\n\n### 1.1 New (P1-REQ-4) {#sec-1.1}\n\nNew text.\n\n## Sources\n\nA list.\n",
			[]arrangedText{
				{heading: "Renamed head", depth: 2},
				{heading: "New", body: "\nNew text.\n", rule: true, ref: "P1-REQ-4", depth: 3, ruleID: 4, ruleVersion: 2},
			},
		},
		"template with appended entries": {
			"---\nstatus: draft\n---\nIntro.\n\n## 3. One {#sec-3}\n\n## 3a Two {#sec-3a}\n",
			[]arrangedText{
				{heading: "One", body: "\nA.\n", rule: true, ref: "P1-REQ-1", depth: 2, ruleID: 1, ruleVersion: 1},
				{heading: "Two", body: "B.", rule: true, ref: "P1-REQ-2", depth: 2, ruleID: 2, ruleVersion: 1},
				{heading: "Three", body: "\r\nC.\r\n", rule: true, ref: "P1-RULE-3", depth: 2, ruleID: 3, ruleVersion: 1},
				{heading: "Open questions", depth: 2, slug: "sec-open-questions"},
			},
		},
		"no arrangement": {"# Plan\n\nfree.\n", nil},
	}
	for name, c := range cases {
		rendered, spans := renderSpans(c.body, c.texts)
		if want := renderBody(c.body, c.texts); rendered != want {
			t.Errorf("%s: rendered %q, renderBody %q", name, rendered, want)
		}
		pos := 0
		for _, sp := range spans {
			if sp.Start != pos || sp.End <= sp.Start {
				t.Fatalf("%s: span %+v does not continue at %d: %+v", name, sp, pos, spans)
			}
			pos = sp.End
			got := rendered[sp.Start:sp.End]
			switch {
			case sp.Rule != 0:
				var src string
				for _, tx := range c.texts {
					if tx.ruleID == sp.Rule {
						src = tx.body
					}
				}
				if want := src[sp.Offset:min(len(src), sp.Offset+len(got))]; got != want {
					t.Errorf("%s: rule span %+v = %q, rule body has %q", name, sp, got, want)
				}
			case sp.Offset >= 0:
				if want := c.body[sp.Offset:min(len(c.body), sp.Offset+len(got))]; got != want {
					t.Errorf("%s: template span %+v = %q, docs.body has %q", name, sp, got, want)
				}
			}
		}
		if pos != len(rendered) {
			t.Errorf("%s: spans end at %d of %d", name, pos, len(rendered))
		}
	}
}

// mapSpecBody has template text (preamble, the "#### Example" section)
// around and between two rules.
const mapSpecBody = "---\nstatus: draft\n---\n# T\n\nPreamble text.\n\n## 1. One {#sec-1}\n\nFirst rule body text.\n\n" +
	"#### Example\n\nTemplate between.\n\n## 2. Two {#sec-2}\n\nSecond rule body.\n"

// TestMapRenderedRange: a range of a spec's rendered body maps onto the rule
// text or the template text it came from; one that leaves its span, covers a
// derived heading line or names an old version is refused; OpenThread stores
// a range on rule text as a rule thread (WL-REQ-1317).
func TestMapRenderedRange(t *testing.T) {
	s := reviewFixture(t)
	doc := mustCreateDoc(t, s, DocInput{Project: "horndb", Kind: "spec", Number: 8, Slug: "map", Body: mapSpecBody, CreatedBy: "stig"})
	var r1 int64
	var r1v int
	var b1, stored string
	if err := s.db.QueryRow(
		`SELECT e.rule_id, v.version, v.body FROM doc_entries e
		   JOIN rule_versions v ON v.rule_id = e.rule_id AND v.version = e.rule_version
		  WHERE e.doc_id = $1 AND e.anchor = 'sec-1'`, doc.ID).Scan(&r1, &r1v, &b1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT body FROM docs WHERE id = $1`, doc.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	rendered := string(doc.Body)
	mapRange := func(version, start, end int) (model.ReviewAnchor, error) {
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return mapRenderedRange(tx, doc.ID, version, start, end)
	}

	bs := strings.Index(b1, "rule body")
	i := strings.Index(rendered, b1[bs:bs+8])
	got, err := mapRange(doc.Version, i, i+8)
	if err != nil || got != (model.ReviewAnchor{Rule: r1, Version: r1v, ByteStart: bs, ByteEnd: bs + 8}) {
		t.Errorf("range in rule text = %+v, %v; want rule %d v%d [%d,%d)", got, err, r1, r1v, bs, bs+8)
	}

	ti := strings.Index(rendered, "Template between")
	got, err = mapRange(doc.Version, ti, ti+len("Template"))
	if err != nil || got.Doc != doc.ID || got.Rule != 0 || got.Version != doc.Version ||
		stored[got.ByteStart:got.ByteEnd] != "Template" {
		t.Errorf("range in template text = %+v, %v; want doc %d v%d over %q", got, err, doc.ID, doc.Version, "Template")
	}

	end := strings.Index(rendered, "text.\n") + len("text.")
	if _, err := mapRange(doc.Version, end-4, ti+3); !errors.Is(err, ErrInvalidInput) ||
		!strings.Contains(err.Error(), "sec-1") || !strings.Contains(err.Error(), "template") {
		t.Errorf("range from rule into template: %v, want ErrInvalidInput naming sec-1 and the template", err)
	}
	h := strings.Index(rendered, "## 1. One")
	if _, err := mapRange(doc.Version, h, h+5); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("range over a heading line: %v, want ErrInvalidInput", err)
	}
	if _, err := mapRange(doc.Version-1, i, i+8); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("old version: %v, want ErrInvalidInput", err)
	}

	th, err := openThread(t, s, 0, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: doc.ID, Version: doc.Version, ByteStart: i, ByteEnd: i + 8, Quote: b1[bs : bs+8]},
		Intent: "note", Body: "rule bytes",
	}, "a")
	if err != nil {
		t.Fatalf("OpenThread on rendered rule text: %v", err)
	}
	var ruleID, docID *int64
	if err := s.db.QueryRow(`SELECT rule_id, doc_id FROM review_threads WHERE id = $1`, th.ID).Scan(&ruleID, &docID); err != nil {
		t.Fatal(err)
	}
	if ruleID == nil || *ruleID != r1 || docID != nil || th.Anchor.DocAnchor != "sec-1" {
		t.Errorf("stored rule_id=%v doc_id=%v anchor=%q, want rule %d at sec-1", ruleID, docID, th.Anchor.DocAnchor, r1)
	}
	th, err = openThread(t, s, 0, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: doc.ID, Version: doc.Version, ByteStart: ti, ByteEnd: ti + 8, Quote: "Template"},
		Intent: "note", Body: "template bytes",
	}, "a")
	if err != nil || th.Anchor.Doc != doc.ID || stored[th.Anchor.ByteStart:th.Anchor.ByteEnd] != "Template" {
		t.Errorf("OpenThread on template text = %+v, %v; want a doc thread into docs.body", th, err)
	}
}
