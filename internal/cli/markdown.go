package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
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
	fd, isTTY := terminalFd(w)
	if !isTTY || !colorEnabled() {
		fmt.Fprintf(w, "%s\n", strings.TrimRight(body, "\n"))
		return
	}
	width, _, err := term.GetSize(fd)
	if err != nil {
		width = 0
	}
	out, err := renderStyled(body, clampWidth(width))
	if err != nil {
		// Styling is a nicety; never lose the body over it.
		fmt.Fprintf(w, "%s\n", strings.TrimRight(body, "\n"))
		return
	}
	fmt.Fprintf(w, "%s\n", strings.TrimRight(out, "\n"))
}

// renderStyled renders body as ANSI-styled markdown wrapped at width. The
// style follows GLAMOUR_STYLE when set, else the terminal's light/dark
// background.
func renderStyled(body string, width int) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithEnvironmentConfig(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return r.Render(body)
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

// colorEnabled honours the two conventions that suppress styling:
// https://no-color.org and TERM=dumb.
func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

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
