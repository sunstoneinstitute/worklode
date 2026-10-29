package model

import "time"

// Review is one review of a subject (WL-REQ-1322): a document, a rule or a
// task, with its reviewer roster, threads and rounds. Reviewers is the
// roster State is derived against.
type Review struct {
	ID        int64          `json:"id"`
	Subject   string         `json:"subject"`
	Task      string         `json:"task,omitempty"`
	Doc       int64          `json:"doc,omitempty"`
	Rule      int64          `json:"rule,omitempty"`
	State     string         `json:"state"`
	BaseRef   string         `json:"base_ref"`
	Round     int            `json:"round"`
	OpenedBy  string         `json:"opened_by"`
	OpenedAt  time.Time      `json:"opened_at"`
	ClosedAt  *time.Time     `json:"closed_at,omitempty"`
	Reviewers []string       `json:"reviewers"`
	Threads   []ReviewThread `json:"threads,omitempty"`
	Rounds    []ReviewRound  `json:"rounds,omitempty"`
}

// ReviewThread is one anchored conversation. IntentClassifier and
// IntentConfidence mirror the intent_classifier and intent_confidence columns
// and are empty when the client stated the intent (WL-REQ-1322).
type ReviewThread struct {
	ID               int64           `json:"id"`
	Review           int64           `json:"review"`
	Anchor           ReviewAnchor    `json:"anchor"`
	Intent           string          `json:"intent"`
	IntentClassifier string          `json:"intent_classifier,omitempty"`
	IntentConfidence float64         `json:"intent_confidence,omitempty"`
	Status           string          `json:"status"`
	Author           string          `json:"author"`
	RaisedByTask     string          `json:"raised_by_task,omitempty"`
	RaisedBySession  string          `json:"raised_by_session,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	ResolvedBy       string          `json:"resolved_by,omitempty"`
	ResolvedAt       *time.Time      `json:"resolved_at,omitempty"`
	Messages         []ReviewMessage `json:"messages,omitempty"`
}

// ReviewMessage is one message in a thread.
type ReviewMessage struct {
	ID        int64     `json:"id"`
	Thread    int64     `json:"thread"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewRound is one submission by a reviewer or the author. Verdict is empty
// on an author round.
type ReviewRound struct {
	Review      int64     `json:"review"`
	Round       int       `json:"round"`
	Actor       string    `json:"actor"`
	Client      string    `json:"client,omitempty"`
	BaseRef     string    `json:"base_ref"`
	Focused     bool      `json:"focused,omitempty"`
	OverallNote string    `json:"overall_note,omitempty"`
	Verdict     string    `json:"verdict,omitempty"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// ReviewAnchor is one field per WL-REQ-1322 anchor column. A document or rule
// anchor sets exactly one of Doc and Rule. A range anchor carries Version,
// ByteStart, ByteEnd and Quote, with DocAnchor derived on write. A
// section-anchored note carries DocAnchor alone (WL-REQ-1667). The change
// fields from Repo onward belong to change reviews.
type ReviewAnchor struct {
	Doc        int64  `json:"doc,omitempty"`
	Rule       int64  `json:"rule,omitempty"`
	Version    int    `json:"version,omitempty"`
	ByteStart  int    `json:"byte_start,omitempty"`
	ByteEnd    int    `json:"byte_end,omitempty"`
	Quote      string `json:"quote,omitempty"`
	DocAnchor  string `json:"doc_anchor,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Path       string `json:"path,omitempty"`
	Side       string `json:"side,omitempty"`
	Line       int    `json:"line,omitempty"`
	AnchorText string `json:"anchor_text,omitempty"`
	BlobSHA    string `json:"blob_sha,omitempty"`
}

// OpenReviewInput is the body that opens a review.
type OpenReviewInput struct {
	Doc     int64  `json:"doc,omitempty"`
	Task    string `json:"task,omitempty"`
	BaseRef string `json:"base_ref,omitempty"`
}

// OpenThreadInput is the body that opens a thread.
type OpenThreadInput struct {
	Anchor ReviewAnchor `json:"anchor"`
	Intent string       `json:"intent"`
	Body   string       `json:"body"`
}

// ThreadReanchor moves a thread to a new anchor.
type ThreadReanchor struct {
	Thread int64        `json:"thread"`
	Anchor ReviewAnchor `json:"anchor"`
}

// ReviewRoundInput is the body of a round. A body with Verdict is a reviewer
// round. A body with BaseRef and no Verdict is an author round.
type ReviewRoundInput struct {
	Verdict     string            `json:"verdict,omitempty"`
	OverallNote string            `json:"overall_note,omitempty"`
	Client      string            `json:"client,omitempty"`
	Focused     bool              `json:"focused,omitempty"`
	Threads     []OpenThreadInput `json:"threads,omitempty"`
	BaseRef     string            `json:"base_ref,omitempty"`
	Reanchors   []ThreadReanchor  `json:"reanchors,omitempty"`
}

// ReviewListParams are the query parameters of the review list route.
type ReviewListParams struct {
	Subject  string `query:"subject"`
	Reviewer string `query:"reviewer"`
	State    string `query:"state"`
}

// DocThreadsParams are the query parameters of a document's thread list.
type DocThreadsParams struct {
	Version int    `query:"version"`
	Anchor  string `query:"anchor"`
	Status  string `query:"status"`
}

// RuleThreadsParams are the query parameters of a rule's thread list.
type RuleThreadsParams struct {
	Version int    `query:"version"`
	Status  string `query:"status"`
}

// ReviewEventsParams are the query parameters of the review events feed.
type ReviewEventsParams struct {
	After   int64 `query:"after"`
	Timeout int   `query:"timeout"`
}
