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

// TestExtractRawHTML covers plan 3's sanitiser: a body that never uses
// markdown image syntax but cites a blob through raw HTML must still pin it.
func TestExtractRawHTML(t *testing.T) {
	h := strings.Repeat("f", 64)
	cases := []struct {
		name string
		body string
	}{
		{name: "html block img", body: "before\n\n<img src=\"/blob/" + h + "\">\n\nafter\n"},
		{name: "inline raw html img in prose", body: "see <img src=\"/blob/" + h + "\"/> below\n"},
		{name: "inline img inside a block-tag paragraph", body: "<p>see <img src=\"/blob/" + h + "\"/></p>\n"},
		{name: "single-quoted src", body: "<img src='/blob/" + h + "'>\n"},
		{name: "unquoted src", body: "<img src=/blob/" + h + ">\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := blobref.Extract(c.body)
			want := []string{h}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Extract(%q) = %v, want %v", c.body, got, want)
			}
		})
	}
}

func TestExtractRawHTMLVideoAndSource(t *testing.T) {
	h1 := strings.Repeat("1", 64)
	h2 := strings.Repeat("2", 64)
	body := "<video src=\"/blob/" + h1 + "\" controls></video>\n\n" +
		"<source src=\"/blob/" + h2 + "\">\n"
	got := blobref.Extract(body)
	want := []string{h1, h2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract = %v, want %v", got, want)
	}
}

func TestExtractDedupesRawHTMLAndMarkdown(t *testing.T) {
	h := strings.Repeat("3", 64)
	body := "![md](/blob/" + h + ")\n\n<img src=\"/blob/" + h + "\">\n"
	got := blobref.Extract(body)
	want := []string{h}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract = %v, want %v (one entry, not two)", got, want)
	}
}

// TestExtractIgnoresRawHTMLInCode: a raw-HTML-looking tag inside a fenced
// code block or inline code span is source text, not a raw-HTML AST node --
// it must not count any more than a markdown image would (see
// TestExtractIgnoresNonImages).
func TestExtractIgnoresRawHTMLInCode(t *testing.T) {
	h := strings.Repeat("e", 64)
	body := "```\n<img src=\"/blob/" + h + "\">\n```\n\n" +
		"`<img src=\"/blob/" + h + "\">` inline\n"
	if got := blobref.Extract(body); len(got) != 0 {
		t.Fatalf("Extract = %v, want none", got)
	}
}

func TestExtractIgnoresMalformedRawHTML(t *testing.T) {
	body := "<img src=\"/blob/" + strings.Repeat("a", 65) + "\">\n\n" + // too long
		"<img src=\"/blob/" + strings.Repeat("b", 63) + "\">\n\n" + // too short
		"<img src=\"/blob/" + strings.Repeat("C", 64) + "\">\n" // uppercase
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

func TestRemoteImages(t *testing.T) {
	body := "![a](https://x.example/y.png)\n\n![dup](https://x.example/y.png)\n\n" +
		"![http](http://x.example/z.png)\n\n![local](./shot.png)\n\n" +
		"![abs](/etc/passwd)\n\n![blob](/blob/" + strings.Repeat("d", 64) + ")\n\n" +
		"![data](data:image/png;base64,AAAA)\n\n" +
		"[link](https://x.example/not-an-image.png)\n"
	got := blobref.RemoteImages(body)
	want := []string{"https://x.example/y.png", "http://x.example/z.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteImages = %v, want %v (document order, deduped)", got, want)
	}
}

// TestRemoteImagesIgnoresRawHTML pins the markdown-only contract: unlike
// Extract, RemoteImages does not report raw-HTML sources, because
// ReplaceDestination cannot rewrite them and mirroring one would leave
// unreferenced bytes in the bucket.
func TestRemoteImagesIgnoresRawHTML(t *testing.T) {
	body := "<img src=\"https://x.example/y.png\">\n\n" +
		"```\n![fenced](https://x.example/z.png)\n```\n"
	if got := blobref.RemoteImages(body); got != nil {
		t.Fatalf("RemoteImages = %v, want none", got)
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
