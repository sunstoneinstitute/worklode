package review

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestState(t *testing.T) {
	round := func(actor, verdict, base string) model.ReviewRound {
		return model.ReviewRound{Actor: actor, Verdict: verdict, BaseRef: base}
	}
	cases := []struct {
		name   string
		roster []string
		rounds []model.ReviewRound
		base   string
		openCR bool
		want   string
	}{
		{"nobody sent", []string{"a", "b"}, nil, "3", false, "open"},
		{"one of two approved", []string{"a", "b"}, []model.ReviewRound{round("a", "approved", "3")}, "3", false, "open"},
		{"all approved", []string{"a", "b"}, []model.ReviewRound{round("a", "approved", "3"), round("b", "approved", "3")}, "3", false, "approved"},
		{"all approved but a change_request is open", []string{"a"}, []model.ReviewRound{round("a", "approved", "3")}, "3", true, "open"},
		{"any changes_requested wins", []string{"a", "b"}, []model.ReviewRound{round("a", "approved", "3"), round("b", "changes_requested", "3")}, "3", false, "changes_requested"},
		{"approval at an old base does not count", []string{"a"}, []model.ReviewRound{round("a", "approved", "2")}, "3", false, "open"},
		{"latest round per reviewer wins", []string{"a"}, []model.ReviewRound{round("a", "changes_requested", "3"), round("a", "approved", "3")}, "3", false, "approved"},
		{"empty roster stays open", nil, nil, "3", false, "open"},
		{"non-roster approval ignored", []string{"a"}, []model.ReviewRound{round("x", "approved", "3")}, "3", false, "open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := State(c.roster, c.rounds, c.base, c.openCR); got != c.want {
				t.Errorf("State = %q, want %q", got, c.want)
			}
		})
	}
}

func TestOpenQuestionAndBlocksApproval(t *testing.T) {
	for _, intent := range []string{"note", "question", "change_request"} {
		for _, status := range []string{"open", "resolved", "obsolete"} {
			th := []model.ReviewThread{{Intent: intent, Status: status}}
			open := status == "open"
			if got, want := OpenQuestion(th), open && intent == "question"; got != want {
				t.Errorf("OpenQuestion(%s,%s) = %v, want %v", intent, status, got, want)
			}
			if got, want := BlocksApproval(th), open && intent == "change_request"; got != want {
				t.Errorf("BlocksApproval(%s,%s) = %v, want %v", intent, status, got, want)
			}
		}
	}
	if OpenQuestion(nil) || BlocksApproval(nil) {
		t.Error("no threads must not report open")
	}
}
