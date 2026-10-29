// reviews.go is the client and the rendering for reviews of documents and
// rules (WL-REQ-1323, WL-REQ-1775): the review, its threads and its rounds.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func reviewPath(id int64, rest string) string {
	return "/api/v1/reviews/" + strconv.FormatInt(id, 10) + rest
}

// OpenReview calls POST /api/v1/reviews.
func (c *Client) OpenReview(ctx context.Context, in model.OpenReviewInput) (model.Review, []byte, error) {
	return doJSON[model.Review](ctx, c, http.MethodPost, "/api/v1/reviews", in, "review")
}

// ListReviews calls GET /api/v1/reviews.
func (c *Client) ListReviews(ctx context.Context, p model.ReviewListParams) ([]model.Review, []byte, error) {
	return doJSON[[]model.Review](ctx, c, http.MethodGet, withParams("/api/v1/reviews", p), nil, "review list")
}

// GetReview calls GET /api/v1/reviews/{id}: the review with its threads,
// their messages, and its rounds.
func (c *Client) GetReview(ctx context.Context, id int64) (model.Review, []byte, error) {
	return doJSON[model.Review](ctx, c, http.MethodGet, reviewPath(id, ""), nil, "review")
}

// OpenReviewThread calls POST /api/v1/reviews/{id}/threads.
func (c *Client) OpenReviewThread(ctx context.Context, id int64, in model.OpenThreadInput) (model.ReviewThread, []byte, error) {
	return doJSON[model.ReviewThread](ctx, c, http.MethodPost, reviewPath(id, "/threads"), in, "thread")
}

// ReplyReviewThread calls POST /api/v1/reviews/{id}/threads/{tid}/messages.
func (c *Client) ReplyReviewThread(ctx context.Context, id, tid int64, body string) (model.ReviewMessage, []byte, error) {
	return doJSON[model.ReviewMessage](ctx, c, http.MethodPost,
		reviewPath(id, "/threads/"+strconv.FormatInt(tid, 10)+"/messages"), model.ThreadReplyInput{Body: body}, "message")
}

// ResolveReviewThread calls POST /api/v1/reviews/{id}/threads/{tid}/resolve.
func (c *Client) ResolveReviewThread(ctx context.Context, id, tid int64, status string) (model.ReviewThread, []byte, error) {
	return doJSON[model.ReviewThread](ctx, c, http.MethodPost,
		reviewPath(id, "/threads/"+strconv.FormatInt(tid, 10)+"/resolve"), model.ThreadResolveInput{Status: status}, "thread")
}

// DocThreads calls GET /api/v1/docs/{id}/threads.
func (c *Client) DocThreads(ctx context.Context, id int64, p model.DocThreadsParams) ([]model.ReviewThread, []byte, error) {
	return doJSON[[]model.ReviewThread](ctx, c, http.MethodGet, withParams(docPath(id, "/threads"), p), nil, "threads")
}

// RuleThreads calls GET /api/v1/rules/{ref}/threads.
func (c *Client) RuleThreads(ctx context.Context, ref string, p model.RuleThreadsParams) ([]model.ReviewThread, []byte, error) {
	return doJSON[[]model.ReviewThread](ctx, c, http.MethodGet,
		withParams("/api/v1/rules/"+url.PathEscape(ref)+"/threads", p), nil, "threads")
}

// AddDocThread calls POST /api/v1/docs/{id}/threads: a bare note.
func (c *Client) AddDocThread(ctx context.Context, id int64, in model.OpenThreadInput) (model.ReviewThread, []byte, error) {
	return doJSON[model.ReviewThread](ctx, c, http.MethodPost, docPath(id, "/threads"), in, "thread")
}

// ReplyThread calls POST /api/v1/threads/{tid}/messages: any thread, a bare
// note included.
func (c *Client) ReplyThread(ctx context.Context, tid int64, body string) (model.ReviewMessage, []byte, error) {
	return doJSON[model.ReviewMessage](ctx, c, http.MethodPost,
		"/api/v1/threads/"+strconv.FormatInt(tid, 10)+"/messages", model.ThreadReplyInput{Body: body}, "message")
}

// ResolveThread calls POST /api/v1/threads/{tid}/resolve: any thread, a bare
// note included. An empty status resolves.
func (c *Client) ResolveThread(ctx context.Context, tid int64, status string) (model.ReviewThread, []byte, error) {
	return doJSON[model.ReviewThread](ctx, c, http.MethodPost,
		"/api/v1/threads/"+strconv.FormatInt(tid, 10)+"/resolve", model.ThreadResolveInput{Status: status}, "thread")
}

// SubmitReviewRound calls POST /api/v1/reviews/{id}/rounds: a reviewer round
// when in.Verdict is set, an author round when in.BaseRef is.
func (c *Client) SubmitReviewRound(ctx context.Context, id int64, in model.ReviewRoundInput) (model.Review, []byte, error) {
	return doJSON[model.Review](ctx, c, http.MethodPost, reviewPath(id, "/rounds"), in, "review")
}

// GetReviewRound calls GET /api/v1/reviews/{id}/rounds/{n}.
func (c *Client) GetReviewRound(ctx context.Context, id int64, n int) (model.ReviewRound, []byte, error) {
	return doJSON[model.ReviewRound](ctx, c, http.MethodGet, reviewPath(id, "/rounds/"+strconv.Itoa(n)), nil, "round")
}

// ReviewEvents long-polls GET /api/v1/reviews/{id}/events. It returns nil
// when the timeout passed with nothing to report (204). The request runs
// without the client's 30s cap, since the server holds it up to the timeout.
func (c *Client) ReviewEvents(ctx context.Context, id int64, p model.ReviewEventsParams) (*model.ReviewEvents, error) {
	long := *c
	long.http = &http.Client{Transport: c.http.Transport}
	raw, err := long.do(ctx, http.MethodGet, withParams(reviewPath(id, "/events"), p), nil)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var ev model.ReviewEvents
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("decode review events: %w", err)
	}
	return &ev, nil
}

// FindQuote returns the byte offset of quote in body, which must occur
// exactly once (WL-REQ-1317). Zero or several hits is an error naming the
// offsets, so the caller can quote more of the text. what names the text.
func FindQuote(body, quote, what string) (int, error) {
	if quote == "" {
		return 0, fmt.Errorf("an empty quote anchors nothing")
	}
	var hits []string
	first := -1
	for i := 0; ; {
		j := strings.Index(body[i:], quote)
		if j < 0 {
			break
		}
		if first < 0 {
			first = i + j
		}
		hits = append(hits, strconv.Itoa(i+j))
		i += j + 1
	}
	switch len(hits) {
	case 0:
		return 0, fmt.Errorf("quote %q does not occur in %s", quote, what)
	case 1:
		return first, nil
	}
	return 0, fmt.Errorf("quote %q occurs %d times in %s, at bytes %s: quote more of the text",
		quote, len(hits), what, strings.Join(hits, ", "))
}

// reviewSubject names a review's subject: "doc 12", "rule 4", or the task.
func reviewSubject(r model.Review) string {
	switch {
	case r.Doc != 0:
		return "doc " + strconv.FormatInt(r.Doc, 10)
	case r.Rule != 0:
		return "rule " + strconv.FormatInt(r.Rule, 10)
	}
	return dash(r.Task)
}

// ReviewTable prints one row per review.
func ReviewTable(w io.Writer, rows []model.Review) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "SUBJECT"},
		column{header: "STATE"},
		column{header: "ROUND"},
		column{header: "OPENED BY"},
		titleColumn("REVIEWERS"),
	)
	for _, r := range rows {
		tbl.add(strconv.FormatInt(r.ID, 10), reviewSubject(r), r.State, strconv.Itoa(r.Round),
			r.OpenedBy, dash(strings.Join(r.Reviewers, ", ")))
	}
	tbl.flush(w)
}

// ReviewVerdicts maps each rostered reviewer to their current verdict: the
// verdict of their latest round at the review's base_ref, or "awaiting"
// (WL-REQ-1777: a verdict on an older base no longer counts).
func ReviewVerdicts(r model.Review) map[string]string {
	out := make(map[string]string, len(r.Reviewers))
	for _, a := range r.Reviewers {
		out[a] = "awaiting"
	}
	for _, rd := range r.Rounds {
		if rd.Verdict != "" && rd.BaseRef == r.BaseRef {
			out[rd.Actor] = rd.Verdict
		}
	}
	return out
}

// ReviewRender writes one review in full: header, the roster with each
// reviewer's current verdict, threads grouped by subject and section with
// their quote and messages, and the rounds.
func ReviewRender(w io.Writer, r model.Review) {
	fmt.Fprintf(w, "review %d  %s  %s  round %d  base %s\n", r.ID, reviewSubject(r), r.State, r.Round, r.BaseRef)
	fmt.Fprintf(w, "  opened %s by %s\n", LocalTime(r.OpenedAt), r.OpenedBy)
	if r.ClosedAt != nil {
		fmt.Fprintf(w, "  closed %s\n", LocalTime(*r.ClosedAt))
	}
	fmt.Fprintln(w, "\nreviewers:")
	verdicts := ReviewVerdicts(r)
	for _, a := range r.Reviewers {
		fmt.Fprintf(w, "  %s  %s\n", a, verdicts[a])
	}
	if len(r.Threads) > 0 {
		fmt.Fprintln(w, "\nthreads:")
	}
	var order []string
	groups := map[string][]model.ReviewThread{}
	for _, t := range r.Threads {
		k := threadSubject(t.Anchor)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], t)
	}
	for _, k := range order {
		fmt.Fprintf(w, "  %s\n", k)
		for _, t := range groups[k] {
			writeThread(w, t, "    ")
		}
	}
	if len(r.Rounds) > 0 {
		fmt.Fprintln(w, "\nrounds:")
	}
	for _, rd := range r.Rounds {
		fmt.Fprintf(w, "  %d  %s  %s  base %s  %s  %s\n", rd.Round, rd.Actor, rd.Verdict, rd.BaseRef,
			dash(rd.Client), LocalTime(rd.SubmittedAt))
		if rd.OverallNote != "" {
			fmt.Fprintf(w, "     %s\n", rd.OverallNote)
		}
	}
}

// ReviewThreadRender writes one thread: its anchor, quote and messages.
func ReviewThreadRender(w io.Writer, t model.ReviewThread) {
	fmt.Fprintf(w, "%s\n", threadSubject(t.Anchor))
	writeThread(w, t, "  ")
}

// threadSubject is a thread's subject and section: "rule 4 #sec-1".
func threadSubject(a model.ReviewAnchor) string {
	s := "doc " + strconv.FormatInt(a.Doc, 10)
	if a.Rule != 0 {
		s = "rule " + strconv.FormatInt(a.Rule, 10)
	}
	if a.Version != 0 {
		s += " v" + strconv.Itoa(a.Version)
	}
	if a.DocAnchor != "" {
		s += " #" + a.DocAnchor
	}
	return s
}

func writeThread(w io.Writer, t model.ReviewThread, indent string) {
	fmt.Fprintf(w, "%sthread %d  %s  %s  by %s\n", indent, t.ID, t.Intent, t.Status, t.Author)
	if t.Anchor.Quote != "" {
		fmt.Fprintf(w, "%s  > %q  [%d,%d)\n", indent, t.Anchor.Quote, t.Anchor.ByteStart, t.Anchor.ByteEnd)
	}
	for _, m := range t.Messages {
		fmt.Fprintf(w, "%s  %d %s (%s) %s: %s\n", indent, m.Seq, m.Author, m.Role, LocalTime(m.CreatedAt),
			strings.ReplaceAll(strings.TrimSpace(m.Body), "\n", "\n"+indent+"    "))
	}
}
