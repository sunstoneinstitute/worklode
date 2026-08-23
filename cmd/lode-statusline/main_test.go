package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRendersStatusLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	var out bytes.Buffer
	if code := run(nil, strings.NewReader(`{"model":{"display_name":"Opus 5"},"workspace":{"current_dir":"`+dir+`"}}`), &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "Opus 5") || strings.Contains(out.String(), "\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunSwallowsInvalidInputAndArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unexpected"}} {
		if code := run(args, strings.NewReader("not json"), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
			t.Fatalf("run(%v) = %d", args, code)
		}
	}
}
