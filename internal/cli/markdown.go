package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// Markdown width bounds. Task bodies are prose, so an unbounded wrap on a wide
// terminal reads badly; the floor keeps code fences legible on a narrow one.
const (
	defaultMarkdownWidth = 80
	minMarkdownWidth     = 40
	maxMarkdownWidth     = 100
)

// Markdown writes body to w, styled when w is an interactive terminal and raw
// otherwise.
//
// Raw is the right default off-TTY, not a degraded one: `lode next` and
// `lode task brief` are read by agents and piped into other tools, and both
// want the markdown source rather than ANSI escapes and reflowed lines.
func Markdown(w io.Writer, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	width, isTTY := termWidth(w)
	if !isTTY || !colorEnabled() {
		fmt.Fprintf(w, "%s\n", strings.TrimRight(body, "\n"))
		return
	}
	fmt.Fprintf(w, "%s\n", renderStyled(body, clampWidth(width)))
}

// blobRef matches a root-relative blob destination in a markdown body.
var blobRef = regexp.MustCompile(`\]\(/blob/([0-9a-f]{64})\)`)

// MarkdownWithBase renders body, first rewriting root-relative /blob/ URLs
// to absolute ones so they are complete and terminal-clickable. The web UI
// resolves them itself; nothing else can.
func MarkdownWithBase(w io.Writer, body, server string) {
	if server != "" {
		body = blobRef.ReplaceAllString(body, "]("+strings.TrimSuffix(server, "/")+"/blob/$1)")
	}
	Markdown(w, body)
}

// terminalFd reports w's file descriptor when w is an interactive terminal.
// Writers without an Fd (buffers in tests, pipes in scripts) are not.
func terminalFd(w io.Writer) (int, bool) {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}

// termWidth reports w's terminal width and whether w is a terminal at all. A
// terminal whose size cannot be read reports width 0, which every caller
// already maps to its own default.
//
// Off-TTY width policy. Every renderer probes through here, and the three that
// wrap answer "how wide when there is no terminal" differently on purpose —
// the rule is whether the content has a natural width of its own:
//
//   - table.flush (table.go) renders unlimited. Its columns take their width
//     from the data, so with no terminal to fit there is nothing to decide,
//     and the agents and pipes parsing `lode task list` want one row per line.
//   - tableWidth (render.go), used only by SkillTable, falls back to 80. A
//     skill description is a paragraph, not a cell: unlimited would leave the
//     column at its minimum (24) and hard-wrap 400-character prose, so the
//     table needs a width even when nothing supplies one.
//   - clampWidth (below) falls back to 80 for the same reason — prose has no
//     natural width — but only ever runs on a terminal whose size failed to
//     read, because Markdown prints raw off-TTY and never reaches it.
//
// Unifying them would change `lode skills` off-TTY output for no gain; the
// divergence is the policy, not an oversight (WL-168).
func termWidth(w io.Writer) (int, bool) {
	fd, isTTY := terminalFd(w)
	if !isTTY {
		return 0, false
	}
	width, _, err := term.GetSize(fd)
	if err != nil {
		return 0, true
	}
	return width, true
}

// colorEnabled honours the two conventions that suppress styling:
// https://no-color.org and TERM=dumb.
func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// clampWidth is the prose wrap width for a given terminal width, bounded by
// the constants above. Width 0 means a terminal that would not report its
// size; see the off-TTY width policy on termWidth for why it lands on 80.
func clampWidth(termWidth int) int {
	switch {
	case termWidth <= 0:
		return defaultMarkdownWidth
	case termWidth < minMarkdownWidth:
		return minMarkdownWidth
	case termWidth > maxMarkdownWidth:
		return maxMarkdownWidth
	default:
		return termWidth
	}
}
