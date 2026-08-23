package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresMigrationsPath(t *testing.T) {
	var out bytes.Buffer
	if code := run(context.Background(), []string{"--dsn", "postgres://example.test/db"}, &out); code == 0 || !strings.Contains(out.String(), "migrations-path") {
		t.Fatalf("run without --migrations-path = %d, %q; want migrations-path error", code, out.String())
	}
}
