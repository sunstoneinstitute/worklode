package blobref_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
)

func TestExtract(t *testing.T) {
	h1 := strings.Repeat("a", 64)
	h2 := strings.Repeat("b", 64)
	body := "before\n\n![one](/blob/" + h1 + ")\n\n![two](/blob/" + h2 + ")\n\n" +
		"![dup](/blob/" + h1 + ")\n"

	got := blobref.Extract(body)
	want := []string{h1, h2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract = %v, want %v (sorted, deduped)", got, want)
	}
}

// TestExtractIgnoresNonImages is the reason this is an AST walk and not a
// regex: a hash in a code fence or a plain link is not a reference, and
// treating it as one would keep bytes alive forever.
func TestExtractIgnoresNonImages(t *testing.T) {
	h := strings.Repeat("c", 64)
	body := "```\n![x](/blob/" + h + ")\n```\n\n" +
		"[a link](/blob/" + h + ")\n\n" +
		"`/blob/" + h + "` inline code\n"
	if got := blobref.Extract(body); len(got) != 0 {
		t.Fatalf("Extract = %v, want none", got)
	}
}

func TestExtractIgnoresMalformed(t *testing.T) {
	body := "![short](/blob/abc)\n\n![remote](https://evil.example/x.png)\n\n" +
		"![upper](/blob/" + strings.Repeat("A", 64) + ")\n"
	if got := blobref.Extract(body); len(got) != 0 {
		t.Fatalf("Extract = %v, want none", got)
	}
}

func TestLocalImages(t *testing.T) {
	body := "![a](./shots/one.png)\n\n![b](two.png)\n\n" +
		"![abs](/etc/passwd)\n\n![remote](https://x.example/y.png)\n\n" +
		"![blob](/blob/" + strings.Repeat("d", 64) + ")\n"
	got := blobref.LocalImages(body)
	want := []string{"./shots/one.png", "two.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LocalImages = %v, want %v", got, want)
	}
}

func TestReplaceDestination(t *testing.T) {
	body := "![a](./one.png)\n\n![b](./one.png)\n\n![c](./two.png)\n"
	got, err := blobref.ReplaceDestination(body, map[string]string{
		"./one.png": "/blob/" + strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatalf("ReplaceDestination: %v", err)
	}
	if strings.Contains(got, "./one.png") {
		t.Fatalf("destination not replaced:\n%s", got)
	}
	if !strings.Contains(got, "./two.png") {
		t.Fatalf("unmapped destination should be left alone:\n%s", got)
	}
	if strings.Count(got, "/blob/") != 2 {
		t.Fatalf("both occurrences should be replaced:\n%s", got)
	}
}

// TestReplaceDestinationSpans covers the shapes a whole-token search misses.
// Each one used to leave a local path in the body while its bytes were
// already uploaded -- a broken image plus a blob nothing references -- or
// rewrote text that is not an image destination at all.
func TestReplaceDestinationSpans(t *testing.T) {
	blob := "/blob/" + strings.Repeat("e", 64)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "title is preserved",
			body: "![a](./a.png \"a title\")\n",
			want: "![a](" + blob + " \"a title\")\n",
		},
		{
			// The bracket form only exists to protect spaces, and the
			// replacement has none, so it is dropped with the old path.
			name: "angle brackets",
			body: "![b](<./my shot.png>)\n",
			want: "![b](" + blob + ")\n",
		},
		{
			// Spec 021 §7: a link to a local file is left alone.
			name: "sibling plain link",
			body: "![a](./a.png)\n\n[dl](./a.png)\n",
			want: "![a](" + blob + ")\n\n[dl](./a.png)\n",
		},
		{
			name: "plain link before the image",
			body: "[dl](./a.png)\n\n![a](./a.png)\n",
			want: "[dl](./a.png)\n\n![a](" + blob + ")\n",
		},
		{
			name: "fenced code block",
			body: "```\n![a](./a.png)\n```\n\n![a](./a.png)\n",
			want: "```\n![a](./a.png)\n```\n\n![a](" + blob + ")\n",
		},
		{
			name: "prose mention",
			body: "see ./a.png below\n\n![a](./a.png)\n",
			want: "see ./a.png below\n\n![a](" + blob + ")\n",
		},
		{
			name: "inline code span",
			body: "`![a](./a.png)` renders as ![a](./a.png)\n",
			want: "`![a](./a.png)` renders as ![a](" + blob + ")\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mapping = map[string]string{"./a.png": blob, "./my shot.png": blob}
			got, err := blobref.ReplaceDestination(c.body, mapping)
			if err != nil {
				t.Fatalf("ReplaceDestination: %v", err)
			}
			if got != c.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, c.want)
			}
		})
	}
}

// TestReplaceDestinationUnlocatable: a reference-style image's destination is
// written at the definition, not at the image, so there is no span to splice.
// Erroring is the only honest answer -- the caller has already uploaded the
// file, and a half-rewritten body points at bytes nobody can resolve.
func TestReplaceDestinationUnlocatable(t *testing.T) {
	body := "![a][r]\n\n[r]: ./a.png\n"
	_, err := blobref.ReplaceDestination(body, map[string]string{
		"./a.png": "/blob/" + strings.Repeat("e", 64),
	})
	if err == nil {
		t.Fatal("expected an error for a destination with no inline span")
	}
	if !strings.Contains(err.Error(), "./a.png") {
		t.Fatalf("error should name the destination: %v", err)
	}
}

func TestEmbeddable(t *testing.T) {
	cases := []struct {
		mediaType string
		want      bool
	}{
		{"image/png", true},
		{"video/mp4", true},
		{"text/plain; charset=utf-8", false},
		{"image/png; charset=binary", true},
	}
	for _, c := range cases {
		if got := blobref.Embeddable(c.mediaType); got != c.want {
			t.Errorf("Embeddable(%q) = %v, want %v", c.mediaType, got, c.want)
		}
	}
}
