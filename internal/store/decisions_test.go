package store

import (
	"errors"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestValidateDecisionSpec(t *testing.T) {
	opts := []model.DecisionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	two := 2
	one := 1
	zero := 0
	four := 4
	now := time.Now()
	decidedAt := &now
	cases := []struct {
		name    string
		spec    model.Decision
		wantErr bool
	}{
		{"single_select with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts}, false},
		{"empty key is refused",
			model.Decision{Key: "", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"key with an uppercase letter is refused",
			model.Decision{Key: "Bad-Key", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"key with an underscore is refused",
			model.Decision{Key: "bad_key", Question: "q", ResponseType: "single_select", Options: opts}, true},
		{"empty question is refused",
			model.Decision{Key: "x", Question: "", ResponseType: "single_select", Options: opts}, true},
		{"unknown response_type is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "maybe", Options: opts}, true},

		{"multi_select with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts}, false},
		{"multi_select without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select"}, true},

		{"single_select_notes with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select_notes", Options: opts}, false},
		{"single_select_notes without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select_notes"}, true},

		{"pick_or_freetext with options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "pick_or_freetext", Options: opts}, false},
		{"pick_or_freetext without options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "pick_or_freetext"}, true},

		{"yes_no without options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no"}, false},
		{"yes_no with options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no", Options: opts}, true},

		{"freetext without options is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "freetext"}, false},
		{"freetext with options is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "freetext", Options: opts}, true},

		{"duplicate option label is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select",
				Options: []model.DecisionOption{{Label: "a"}, {Label: "a"}}}, true},
		{"empty option label is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select",
				Options: []model.DecisionOption{{Label: "a"}, {Label: ""}}}, true},

		{"min_picks and max_picks on multi_select is valid",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &one, MaxPicks: &two}, false},
		{"min_picks on single_select is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts,
				MinPicks: &one}, true},
		{"max_picks on single_select is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "single_select", Options: opts,
				MaxPicks: &one}, true},
		{"min_picks below 1 is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &zero}, true},
		{"max_picks below 1 is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MaxPicks: &zero}, true},
		{"min_picks above max_picks is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MinPicks: &two, MaxPicks: &one}, true},
		{"max_picks above len(options) is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "multi_select", Options: opts,
				MaxPicks: &four}, true},

		{"posed answer is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no",
				Answer: &model.DecisionAnswer{Value: "yes"}}, true},
		{"posed decided_at is refused",
			model.Decision{Key: "x", Question: "q", ResponseType: "yes_no",
				DecidedAt: decidedAt}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDecisionSpec(tc.spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateDecisionSpec: %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error %v is not ErrInvalidInput", err)
			}
		})
	}
}

func TestValidateAnswer(t *testing.T) {
	opts := []model.DecisionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	two := 2
	cases := []struct {
		name    string
		spec    model.Decision
		answer  model.DecisionAnswer
		wantErr bool
	}{
		{"single_select picks one offered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"b"}}, false},
		{"single_select refuses an unoffered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"z"}}, true},
		{"single_select refuses smuggled notes",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Notes: "why not"}, true},
		{"single_select refuses more than one pick",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a", "b"}}, true},

		{"multi_select within max_picks",
			model.Decision{ResponseType: "multi_select", Options: opts, MaxPicks: &two},
			model.DecisionAnswer{Picked: []string{"a", "c"}}, false},
		{"multi_select refuses more than max_picks",
			model.Decision{ResponseType: "multi_select", Options: opts, MaxPicks: &two},
			model.DecisionAnswer{Picked: []string{"a", "b", "c"}}, true},
		{"multi_select refuses a repeated pick",
			model.Decision{ResponseType: "multi_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"a", "a"}}, true},
		{"multi_select refuses an empty pick under the default minimum",
			model.Decision{ResponseType: "multi_select", Options: opts},
			model.DecisionAnswer{Picked: []string{}}, true},

		{"single_select_notes picks one and gives notes",
			model.Decision{ResponseType: "single_select_notes", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Notes: "because"}, false},
		{"single_select_notes refuses empty notes",
			model.Decision{ResponseType: "single_select_notes", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}}, true},

		{"pick_or_freetext accepts a pick",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}}, false},
		{"pick_or_freetext accepts freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Freetext: "something else"}, false},
		{"pick_or_freetext refuses both a pick and freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{Picked: []string{"a"}, Freetext: "something else"}, true},
		{"pick_or_freetext refuses neither a pick nor freetext",
			model.Decision{ResponseType: "pick_or_freetext", Options: opts},
			model.DecisionAnswer{}, true},

		{"yes_no takes the third value",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "unsure"}, false},
		{"yes_no refuses smuggled freetext",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "yes", Freetext: "but"}, true},
		{"yes_no refuses a value outside yes/no/unsure",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "maybe"}, true},

		{"freetext accepts non-empty text",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{Freetext: "here is my answer"}, false},
		{"freetext refuses empty text",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{}, true},
		{"freetext refuses smuggled picked",
			model.Decision{ResponseType: "freetext"},
			model.DecisionAnswer{Freetext: "here", Picked: []string{"a"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnswer(tc.spec, tc.answer)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAnswer: %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error %v is not ErrInvalidInput", err)
			}
		})
	}
}
