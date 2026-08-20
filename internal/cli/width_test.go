package cli

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The three off-TTY width policies documented on termWidth (markdown.go).
// They diverge on purpose; this test is what keeps that comment honest, so a
// failure here means the policy changed, not that a number needs nudging.

func TestOffTTYSharedTableIsUnlimited(t *testing.T) {
	var b strings.Builder
	title := "Word-wrap the title column so board rows never wrap the terminal"
	boardLike(title, "agent-1 (until 2026-08-16T12:00:00+02:00)").flush(&b)
	got := lines(b.String())
	if len(got) != 2 {
		t.Fatalf("want header + 1 row off-TTY, got %d lines:\n%s", len(got), b.String())
	}
	if !strings.Contains(got[1], title) {
		t.Fatalf("title was wrapped off-TTY; parsers expect one row per line:\n%s", b.String())
	}
}

func TestOffTTYSkillTableFallsBackTo80(t *testing.T) {
	var b strings.Builder
	if got := tableWidth(&b); got != defaultTableWidth {
		t.Fatalf("tableWidth off-TTY = %d, want %d", got, defaultTableWidth)
	}
	SkillTable(&b, []model.Skill{{
		Name:        "worklode-migrations",
		Description: strings.TrimSpace(strings.Repeat("trigger prose that runs well past any terminal ", 12)),
	}})
	if w := widest(lines(b.String())); w > defaultTableWidth {
		t.Fatalf("skill table off-TTY widest line = %d, want <= %d:\n%s", w, defaultTableWidth, b.String())
	}
}

func TestOffTTYMarkdownFallbackWidth(t *testing.T) {
	// Reached only on a terminal that would not report its size: Markdown
	// prints raw off-TTY, so clampWidth never sees a non-terminal writer.
	if got := clampWidth(0); got != defaultMarkdownWidth {
		t.Fatalf("clampWidth(0) = %d, want %d", got, defaultMarkdownWidth)
	}
	var b strings.Builder
	Markdown(&b, "# Heading\n\nBody text.\n")
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatalf("Markdown styled a non-terminal writer:\n%q", b.String())
	}
}
