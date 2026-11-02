// Package critjson is the golden copy of crit's finish payload (WL-REQ-1773).
// The types mirror the crit tag named in PIN; testdata/<tag>/upstream holds
// the source they were read from, and scripts/check-crit-contract.sh diffs
// that copy against upstream when the pin moves.
package critjson

// Finish is the JSON crit writes to an on_finish_* hook's stdin
// (hooks.JSONPayload). Approved is UnresolvedCount == 0.
type Finish struct {
	ReviewPath             string          `json:"review_path"`
	CommentsCmd            string          `json:"comments_cmd"`
	CommentsAllCmd         string          `json:"comments_all_cmd"`
	NextRoundCmd           string          `json:"next_round_cmd"`
	SessionKey             string          `json:"session_key"`
	Mode                   string          `json:"mode"`
	UnresolvedCount        int             `json:"unresolved_count"`
	TotalCount             int             `json:"total_count"`
	Approved               bool            `json:"approved"`
	PlanSlug               string          `json:"plan_slug"`
	InternalSessionMode    string          `json:"internal_session_mode"`
	PlanHook               string          `json:"plan_hook"`
	FilesWithComments      []string        `json:"files_with_comments"`
	CommentsUnresolvedJSON []ListedComment `json:"comments_unresolved_json"`
	CommentsJSON           []ListedComment `json:"comments_json"`
	SessionStats           *SessionStats   `json:"session_stats,omitempty"`
}

// SessionStats is the optional session_stats object of the finish payload.
type SessionStats struct {
	DurationSeconds   int `json:"duration_seconds"`
	FilesReviewed     int `json:"files_reviewed"`
	CommentsSubmitted int `json:"comments_submitted"`
}

// ListedComment is one element of comments_json and comments_unresolved_json
// (comment.ListedComment): a Comment plus its scope ("review", "file" or
// "line") and, for file and line comments, its file path.
type ListedComment struct {
	Scope string  `json:"scope"`
	Path  *string `json:"path,omitempty"`
	Comment
}

// Comment mirrors session.Comment.
type Comment struct {
	ID                 string     `json:"id"`
	StartLine          int        `json:"start_line"`
	EndLine            int        `json:"end_line"`
	Side               string     `json:"side,omitempty"`
	Body               string     `json:"body"`
	Quote              string     `json:"quote,omitempty"`
	QuoteOffset        *int       `json:"quote_offset,omitempty"`
	Anchor             string     `json:"anchor,omitempty"`
	Drifted            bool       `json:"drifted,omitempty"`
	DriftedOnRound     int        `json:"drifted_on_round,omitempty"`
	Author             string     `json:"author,omitempty"`
	UserID             string     `json:"user_id,omitempty"`
	Scope              string     `json:"scope,omitempty"`
	CreatedAt          string     `json:"created_at"`
	UpdatedAt          string     `json:"updated_at"`
	Resolved           bool       `json:"resolved,omitempty"`
	ResolvedRound      int        `json:"resolved_round,omitempty"`
	Live               bool       `json:"live,omitempty"`
	CarriedForward     bool       `json:"carried_forward,omitempty"`
	ReviewRound        int        `json:"review_round,omitempty"`
	Replies            []Reply    `json:"replies,omitempty"`
	GitHubID           int64      `json:"github_id,omitempty"`
	GitLabNoteID       int64      `json:"gitlab_note_id,omitempty"`
	GitLabDiscussionID string     `json:"gitlab_discussion_id,omitempty"`
	GitLabResolved     *bool      `json:"gitlab_resolved,omitempty"`
	LastPushedBodyHash string     `json:"last_pushed_body_hash,omitempty"`
	HeadSHA            string     `json:"head_sha,omitempty"`
	DiffScope          string     `json:"diff_scope,omitempty"`
	DOMAnchor          *DOMAnchor `json:"dom_anchor,omitempty"`
	PinNumber          int        `json:"pin_number,omitempty"`
	FocusKey           string     `json:"focus_key,omitempty"`
}

// Reply mirrors session.Reply.
type Reply struct {
	ID                 string `json:"id"`
	Body               string `json:"body"`
	Author             string `json:"author,omitempty"`
	UserID             string `json:"user_id,omitempty"`
	CreatedAt          string `json:"created_at"`
	ReviewRound        int    `json:"review_round,omitempty"`
	ResolvedRound      int    `json:"resolved_round,omitempty"`
	GitHubID           int64  `json:"github_id,omitempty"`
	GitLabNoteID       int64  `json:"gitlab_note_id,omitempty"`
	GitLabDiscussionID string `json:"gitlab_discussion_id,omitempty"`
	LastPushedBodyHash string `json:"last_pushed_body_hash,omitempty"`
}

// DOMAnchor mirrors session.DOMAnchor, set only on live-mode pins.
type DOMAnchor struct {
	Pathname       string   `json:"pathname"`
	CSSSelector    string   `json:"css_selector"`
	TagChain       []string `json:"tag_chain"`
	AccessibleName string   `json:"accessible_name,omitempty"`
	Role           string   `json:"role,omitempty"`
	Landmark       string   `json:"landmark,omitempty"`
	OuterHTML      string   `json:"outer_html"`
	ViewportWidth  int      `json:"viewport_width"`
	ViewportHeight int      `json:"viewport_height"`
}
