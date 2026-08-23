package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunVersionDoesNotStartServer(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out); err != nil {
		t.Fatalf("run --version: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("run --version wrote no version")
	}
}
