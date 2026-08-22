package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- skills -----------------------------------------------------------

// Skills calls GET /api/v1/skills.
func (c *Client) Skills(ctx context.Context) ([]model.Skill, []byte, error) {
	resp, raw, err := doJSON[model.SkillsListResponse](ctx, c, http.MethodGet, "/api/v1/skills", nil, "skills")
	if err != nil {
		return nil, nil, err
	}
	return resp.Skills, raw, nil
}

// Skill calls GET /api/v1/skills/{name}.
func (c *Client) Skill(ctx context.Context, name string) (model.Skill, []byte, error) {
	return doJSON[model.Skill](ctx, c, http.MethodGet, "/api/v1/skills/"+url.PathEscape(name), nil, "skill")
}

// SkillArchive calls GET /api/v1/skills/{name}/archive/{hash} and returns the
// raw tar.gz bytes. Unlike every other client method, the response body is
// not JSON — c.do returns the response body untouched regardless of content
// type, so no decode step is needed or possible here.
func (c *Client) SkillArchive(ctx context.Context, name, hash string) ([]byte, error) {
	return c.do(ctx, http.MethodGet,
		"/api/v1/skills/"+url.PathEscape(name)+"/archive/"+url.PathEscape(hash), nil)
}

// RecommendSkills calls POST /api/v1/skills/recommend. Exactly one of taskID
// or text is required by the server.
func (c *Client) RecommendSkills(ctx context.Context, taskID, text string, limit int) (model.SkillRecommendation, []byte, error) {
	in := model.RecommendInput{TaskID: taskID, Text: text, Limit: limit}
	return doJSON[model.SkillRecommendation](ctx, c, http.MethodPost, "/api/v1/skills/recommend", in, "skill recommendation")
}

// SyncSkills calls POST /api/v1/skills/sync (admin-only).
func (c *Client) SyncSkills(ctx context.Context) (model.SkillSyncReport, []byte, error) {
	return doJSON[model.SkillSyncReport](ctx, c, http.MethodPost, "/api/v1/skills/sync", nil, "skill sync report")
}

// SkillSyncRender prints a one-line summary of a skill sync report, then one
// "error:" line per per-source failure (real work can still have happened
// alongside those, per SkillSyncReport's doc comment).
func SkillSyncRender(w io.Writer, report model.SkillSyncReport) {
	fmt.Fprintf(w, "synced %d skill(s): %d changed, %d deleted, %d embedded\n",
		report.Synced, report.Changed, report.Deleted, report.Embedded)
	for _, e := range report.Errors {
		fmt.Fprintf(w, "  error: %s\n", e)
	}
}

// Skill table layout. A skill description is a paragraph of trigger prose, not
// a table cell — several run past 400 characters — so the description column
// wraps to the terminal instead of overflowing it. The name column is capped
// so one long skill name cannot squeeze the prose into a ribbon.
const (
	maxSkillNameWidth = 32
	minSkillDescWidth = 24
)

// SkillTable prints one row per skill: name, then the description wrapped to
// the terminal width with continuation lines aligned under the first.
func SkillTable(w io.Writer, skills []model.Skill) {
	skillTable(w, skills, tableWidth(w))
}

func skillTable(w io.Writer, skills []model.Skill, width int) {
	name := len("NAME")
	for _, sk := range skills {
		name = max(name, displayWidth(sk.Name))
	}
	name = min(name, maxSkillNameWidth)
	desc := max(width-name-2, minSkillDescWidth)

	fmt.Fprintf(w, "%-*s  %s\n", name, "NAME", "DESCRIPTION")
	for _, sk := range skills {
		lines := wrapSkillDesc(sk.Description, desc)
		if len(lines) == 0 {
			lines = []string{""}
		}
		// A name past the cap would push its own description right and break
		// the column; give it the row to itself instead.
		if displayWidth(sk.Name) > name {
			fmt.Fprintln(w, sk.Name)
		} else {
			fmt.Fprintf(w, "%-*s  %s\n", name, sk.Name, lines[0])
			lines = lines[1:]
		}
		for _, l := range lines {
			fmt.Fprintf(w, "%-*s  %s\n", name, "", l)
		}
	}
}

// wrapSkillDesc breaks s into lines of at most width columns, splitting on
// whitespace only. A word longer than width gets a line of its own rather than
// being cut: skill prose carries URLs and backticked identifiers that are
// worse mangled than overlong. The table's own wrapper (wrapWordsAt) hard-
// splits instead, which is why the skill table does not share it.
func wrapSkillDesc(s string, width int) []string {
	var lines []string
	var cur, curWidth = "", 0
	for _, word := range strings.Fields(s) {
		n := displayWidth(word)
		switch {
		case cur == "":
			cur, curWidth = word, n
		case curWidth+1+n <= width:
			cur += " " + word
			curWidth += 1 + n
		default:
			lines = append(lines, cur)
			cur, curWidth = word, n
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// tableWidth is the column count SkillTable renders to: the terminal's when w
// is one, else a conventional 80 so piped and captured output stays stable.
// It does not go unlimited off-TTY the way table.flush does, because a skill
// description has no natural width to fall back to — see the off-TTY width
// policy on termWidth (markdown.go).
func tableWidth(w io.Writer) int {
	width, isTTY := termWidth(w)
	if !isTTY || width <= 0 {
		return defaultTableWidth
	}
	return max(width, minTableWidth)
}

const (
	defaultTableWidth = 80
	minTableWidth     = 40
)
