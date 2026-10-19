package store

import (
	"errors"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestIdeaFindings covers WL-REQ-1800: findings exist for shared ideas only,
// litmus tests list newest date first, and pre-research reads the newest run.
func TestIdeaFindings(t *testing.T) {
	t.Parallel()
	s, ctx := openIdeaStore(t)
	idea, err := s.CreateIdea(ctx, "a", model.CreateIdeaInput{Title: "Rivers", Type: "general"})
	if err != nil {
		t.Fatal(err)
	}
	lt := model.CreateLitmusTestInput{Date: "2026-10-01", Summary: "ok"}
	pr := model.PutPreResearchInput{Date: "2026-10-01", Findings: []string{"f"}}

	// Private: ErrNotShared to its author, ErrNotFound to anyone else; an unknown idea ErrNotFound.
	for _, c := range []struct {
		caller, id string
		want       error
	}{{"a", idea.ID, ErrNotShared}, {"b", idea.ID, ErrNotFound}, {"", idea.ID, ErrNotFound}, {"a", "idea_nope", ErrNotFound}} {
		if _, err := s.ListLitmusTests(ctx, c.caller, c.id); !errors.Is(err, c.want) {
			t.Errorf("%q list litmus %s = %v, want %v", c.caller, c.id, err, c.want)
		}
		if _, err := s.AddLitmusTest(ctx, c.caller, c.id, lt); !errors.Is(err, c.want) {
			t.Errorf("%q add litmus %s = %v, want %v", c.caller, c.id, err, c.want)
		}
		if _, err := s.LatestPreResearch(ctx, c.caller, c.id); !errors.Is(err, c.want) {
			t.Errorf("%q pre-research %s = %v, want %v", c.caller, c.id, err, c.want)
		}
		if _, err := s.PutPreResearch(ctx, c.caller, c.id, pr); !errors.Is(err, c.want) {
			t.Errorf("%q put pre-research %s = %v, want %v", c.caller, c.id, err, c.want)
		}
	}

	if _, err := s.ShareIdea(ctx, "a", idea.ID, "intake"); err != nil {
		t.Fatal(err)
	}

	// Empty cases.
	if l, err := s.ListLitmusTests(ctx, "b", idea.ID); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("empty litmus = %#v, %v", l, err)
	}
	if p, err := s.LatestPreResearch(ctx, "b", idea.ID); err != nil || p.Findings == nil || len(p.Findings) != 0 {
		t.Fatalf("empty pre-research = %#v, %v", p, err)
	}

	// Validation.
	var fe *FieldError
	if _, err := s.AddLitmusTest(ctx, "b", idea.ID, model.CreateLitmusTestInput{Date: "yesterday"}); !errors.As(err, &fe) || fe.Field != "date" {
		t.Fatalf("bad date = %v, want FieldError on date", err)
	}
	if _, err := s.PutPreResearch(ctx, "b", idea.ID, model.PutPreResearchInput{}); !errors.As(err, &fe) || fe.Field != "date" {
		t.Fatalf("missing date = %v, want FieldError on date", err)
	}

	// Newest date first, whatever the insert order.
	for _, d := range []string{"2026-10-02", "2026-10-05", "2026-10-03"} {
		got, err := s.AddLitmusTest(ctx, "b", idea.ID, model.CreateLitmusTestInput{Date: d, Summary: "s" + d, Recommendations: []string{"r" + d}})
		if err != nil {
			t.Fatalf("add %s: %v", d, err)
		}
		if got.ID == "" || got.Date != d || got.Summary != "s"+d || !slices.Equal(got.Recommendations, []string{"r" + d}) {
			t.Fatalf("added = %+v", got)
		}
	}
	wantPayload(t, s, "idea.finding_recorded", map[string]any{"idea": idea.ID, "actor": "b", "kind": "litmus"})
	l, err := s.ListLitmusTests(ctx, "a", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	var dates []string
	for _, x := range l {
		dates = append(dates, x.Date)
	}
	if !slices.Equal(dates, []string{"2026-10-05", "2026-10-03", "2026-10-02"}) {
		t.Fatalf("dates = %v", dates)
	}
	if noRec, err := s.AddLitmusTest(ctx, "b", idea.ID, model.CreateLitmusTestInput{Date: "2026-09-01"}); err != nil || noRec.Recommendations == nil {
		t.Fatalf("no recommendations = %#v, %v; want []", noRec, err)
	}

	// Pre-research: the newest run wins; an older-dated put does not replace it.
	for _, p := range []model.PutPreResearchInput{
		{Date: "2026-10-04", Findings: []string{"new"}},
		{Date: "2026-10-01", Findings: []string{"old"}},
	} {
		if _, err := s.PutPreResearch(ctx, "b", idea.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	wantPayload(t, s, "idea.finding_recorded", map[string]any{"idea": idea.ID, "actor": "b", "kind": "pre_research"})
	if p, err := s.LatestPreResearch(ctx, "a", idea.ID); err != nil || !slices.Equal(p.Findings, []string{"new"}) {
		t.Fatalf("latest = %+v, %v", p, err)
	}
	if p, err := s.PutPreResearch(ctx, "b", idea.ID, model.PutPreResearchInput{Date: "2026-10-04", Findings: []string{"newer"}}); err != nil || !slices.Equal(p.Findings, []string{"newer"}) {
		t.Fatalf("same-date put = %+v, %v; want the later row", p, err)
	}
}
