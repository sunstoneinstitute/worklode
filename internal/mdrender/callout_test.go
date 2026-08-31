package mdrender

import (
	"bytes"
	"strings"
	"testing"
)

// renderTaskRaw runs the task pipeline's parser and HTML renderer without
// the sanitiser. Plan doc 175 task 2 (not yet landed in this tree) adds the
// bluemonday allowlist rule that lets aside/p class survive Body(); until
// then Body() strips both classes, so asserting the renderer's own markup
// through Body() would test the sanitiser (which strips it) rather than
// this task's transformer and renderer. The fallback-to-blockquote cases
// don't need this — blockquote has no class either way — but every table
// here uses it for consistency.
func renderTaskRaw(t *testing.T, body string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := taskFlavour.md.Convert([]byte(body), &buf, withProjectKeys(ProjectKeys{})); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return buf.String()
}

// TestCalloutKinds covers each of the five kinds: aside + title + body, and
// case-insensitive input mapping to the canonical spelling.
func TestCalloutKinds(t *testing.T) {
	cases := []struct {
		kind, marker, title string
	}{
		{"note", "[!NOTE]", "Note"},
		{"tip", "[!TIP]", "Tip"},
		{"important", "[!IMPORTANT]", "Important"},
		{"warning", "[!WARNING]", "Warning"},
		{"caution", "[!CAUTION]", "Caution"},
		{"note", "[!note]", "Note"}, // case-insensitive input, canonical output
	}
	for _, c := range cases {
		t.Run(c.marker, func(t *testing.T) {
			got := renderTaskRaw(t, "> "+c.marker+"\n> Body text.\n")
			if !strings.Contains(got, `<aside class="callout callout-`+c.kind+`">`) {
				t.Errorf("missing aside class for %s in:\n%s", c.kind, got)
			}
			if !strings.Contains(got, `<p class="callout-title">`+c.title+"</p>") {
				t.Errorf("missing title %q in:\n%s", c.title, got)
			}
			if !strings.Contains(got, "Body text.") {
				t.Errorf("missing body in:\n%s", got)
			}
			if strings.Contains(got, "<blockquote") {
				t.Errorf("a literal blockquote survived:\n%s", got)
			}
		})
	}
}

// TestCalloutFallsThrough covers the three ways a blockquote fails to match
// and must render exactly as today's plain blockquote.
func TestCalloutFallsThrough(t *testing.T) {
	cases := map[string]string{
		"unknown kind":                             "> [!FOO]\n> Body.\n",
		"trailing marker text":                     "> [!NOTE] extra words\n> Body.\n",
		"marker not first line of first paragraph": "> Body.\n> [!NOTE]\n> More.\n",
		"marker not in first paragraph":            "> Plain first paragraph.\n>\n> [!NOTE]\n> Second paragraph body.\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := renderTaskRaw(t, body)
			if !strings.Contains(got, "<blockquote>") {
				t.Errorf("did not fall through to a plain blockquote:\n%s", got)
			}
			if strings.Contains(got, "callout") {
				t.Errorf("callout markup leaked through:\n%s", got)
			}
		})
	}
}

// TestCalloutRendersNestedMarkdown pins that a callout's body renders
// exactly as it would inside an ordinary blockquote: a list, a code fence,
// a link.
func TestCalloutRendersNestedMarkdown(t *testing.T) {
	body := "> [!TIP]\n> - one\n> - two\n>\n> ```\n> code\n> ```\n>\n> [a link](/x)\n"
	got := renderTaskRaw(t, body)
	for _, want := range []string{
		"<li>one</li>",
		"<li>two</li>",
		"<pre><code>code\n</code></pre>",
		`<a href="/x">a link</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `<aside class="callout callout-tip">`) {
		t.Errorf("missing callout wrapper:\n%s", got)
	}
}

// TestCalloutNesting: a callout inside a callout renders the outer one and
// leaves the inner blockquote alone unless it, too, matches.
func TestCalloutNesting(t *testing.T) {
	t.Run("inner matches too", func(t *testing.T) {
		body := "> [!NOTE]\n> outer text\n> > [!TIP]\n> > inner text\n"
		got := renderTaskRaw(t, body)
		if !strings.Contains(got, `<aside class="callout callout-note">`) {
			t.Errorf("missing outer callout:\n%s", got)
		}
		if !strings.Contains(got, `<aside class="callout callout-tip">`) {
			t.Errorf("missing inner callout:\n%s", got)
		}
		if strings.Contains(got, "<blockquote") {
			t.Errorf("a blockquote survived when both levels matched:\n%s", got)
		}
	})

	t.Run("inner is a plain blockquote", func(t *testing.T) {
		body := "> [!NOTE]\n> outer text\n> > plain quote\n"
		got := renderTaskRaw(t, body)
		if !strings.Contains(got, `<aside class="callout callout-note">`) {
			t.Errorf("missing outer callout:\n%s", got)
		}
		if strings.Contains(got, "callout-tip") {
			t.Errorf("inner blockquote was mistaken for a callout:\n%s", got)
		}
		if !strings.Contains(got, "<blockquote>") || !strings.Contains(got, "plain quote") {
			t.Errorf("inner blockquote was not left alone:\n%s", got)
		}
	})
}
