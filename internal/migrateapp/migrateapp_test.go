package migrateapp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunValidatesRequiredOptions(t *testing.T) {
	for _, tc := range []struct {
		name, dsn, path, want string
	}{
		{name: "dsn", want: "no DSN"},
		{name: "migrations path", dsn: "postgres://example.test/db", want: "migrations-path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(context.Background(), tc.dsn, tc.path, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run() = %v, want %q error", err, tc.want)
			}
		})
	}
}
