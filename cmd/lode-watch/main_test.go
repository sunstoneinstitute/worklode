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
