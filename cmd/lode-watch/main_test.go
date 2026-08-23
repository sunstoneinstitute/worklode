package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresCluster(t *testing.T) {
	err := run(context.Background(), nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cluster") {
		t.Fatalf("run without --cluster = %v, want cluster error", err)
	}
}
