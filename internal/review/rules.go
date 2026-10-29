// Package review holds the pure rules of a review: derived state, the
// question gate and the approval gate. No database, no HTTP.
package review

import "github.com/sunstoneinstitute/worklode/internal/model"

func hasOpen(threads []model.ReviewThread, intent string) bool {
	for _, t := range threads {
		if t.Status == "open" && t.Intent == intent {
			return true
		}
	}
	return false
}

// OpenQuestion reports whether any question thread is still open, the
// condition under which an author round is refused (WL-REQ-1774).
func OpenQuestion(threads []model.ReviewThread) bool {
	return hasOpen(threads, "question")
}

// BlocksApproval reports whether any change_request thread is open. A note
// or question never blocks (WL-REQ-1777).
func BlocksApproval(threads []model.ReviewThread) bool {
	return hasOpen(threads, "change_request")
}

// State derives reviews.state from the roster and the rounds cast at the
// current base_ref. Rounds at an older base_ref do not count (WL-REQ-1777).
func State(roster []string, rounds []model.ReviewRound, baseRef string, openChangeRequest bool) string {
	latest := map[string]string{}
	for _, r := range rounds {
		if r.BaseRef == baseRef {
			latest[r.Actor] = r.Verdict // rounds arrive oldest first
		}
	}
	approved := 0
	for _, a := range roster {
		switch latest[a] {
		case "changes_requested":
			return "changes_requested"
		case "approved":
			approved++
		}
	}
	if len(roster) > 0 && approved == len(roster) && !openChangeRequest {
		return "approved"
	}
	return "open"
}
