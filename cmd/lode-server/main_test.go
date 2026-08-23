package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunVersionDoesNotStartServer(t *testing.T) {
	var out bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &out); code != 0 {
		t.Fatalf("run --version = %d, want 0", code)
	}
	if out.Len() == 0 {
		t.Fatal("run --version wrote no version")
	}
}
