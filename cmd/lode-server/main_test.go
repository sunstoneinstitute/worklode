package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunVersionDoesNotStartServer(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run --version = %d, want 0", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("run --version wrote no version")
	}
	if stderr.Len() != 0 {
		t.Fatalf("run --version stderr = %q", stderr.String())
	}
}

func TestRunDiagnosticsDoNotUseStdout(t *testing.T) {
	t.Setenv("LODE_DSN", "")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code == 0 {
		t.Fatal("run without a DSN succeeded")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "no DSN") {
		t.Fatalf("stdout=%q stderr=%q, want DSN diagnostic on stderr", stdout.String(), stderr.String())
	}
}
