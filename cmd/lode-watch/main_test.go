package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresCluster(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code == 0 {
		t.Fatal("run without --cluster succeeded")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "cluster") {
		t.Fatalf("stdout=%q stderr=%q, want cluster diagnostic on stderr", stdout.String(), stderr.String())
	}
}

func TestRunArtifactsModeRequiresToken(t *testing.T) {
	t.Setenv("LODE_TOKEN", "")
	var stdout, stderr bytes.Buffer
	args := []string{"-mode", "artifacts", "-server", "http://example.test"}
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("run(%v) = %d, want 2", args, code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "token") {
		t.Fatalf("stdout=%q stderr=%q, want token diagnostic on stderr", stdout.String(), stderr.String())
	}
}

func TestRunArtifactsModeRequiresServer(t *testing.T) {
	t.Setenv("LODE_SERVER", "")
	var stdout, stderr bytes.Buffer
	args := []string{"-mode", "artifacts", "-token", "wl_test"}
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("run(%v) = %d, want 2", args, code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "server") {
		t.Fatalf("stdout=%q stderr=%q, want server diagnostic on stderr", stdout.String(), stderr.String())
	}
}

func TestRunUnknownMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"-mode", "bogus"}
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("run(%v) = %d, want 2", args, code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "--mode") {
		t.Fatalf("stdout=%q stderr=%q, want mode diagnostic on stderr", stdout.String(), stderr.String())
	}
}
