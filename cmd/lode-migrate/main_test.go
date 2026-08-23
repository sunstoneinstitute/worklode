package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresMigrationsPath(t *testing.T) {
	t.Setenv("LODE_DSN", "")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != 1 {
		t.Fatalf("run without --migrations-path = %d, want 1", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `required flag(s) "migrations-path" not set`) || strings.Contains(stderr.String(), "DSN") {
		t.Fatalf("stdout=%q stderr=%q, want only the required-path diagnostic on stderr", stdout.String(), stderr.String())
	}
}

func TestRunValidatesDSNAfterMigrationsPath(t *testing.T) {
	t.Setenv("LODE_DSN", "")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--migrations-path", "migrations"}, &stdout, &stderr); code == 0 {
		t.Fatal("run without a DSN succeeded")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "no DSN") {
		t.Fatalf("stdout=%q stderr=%q, want DSN diagnostic on stderr", stdout.String(), stderr.String())
	}
}
