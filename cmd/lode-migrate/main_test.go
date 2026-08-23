package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresMigrationsPath(t *testing.T) {
	err := run(context.Background(), nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "migrations-path") {
		t.Fatalf("run without --migrations-path = %v, want migrations-path error", err)
	}
}
