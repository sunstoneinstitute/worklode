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
	got := blobref.ReplaceDestination(body, map[string]string{
		"./one.png": "/blob/" + strings.Repeat("e", 64),
	})
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
