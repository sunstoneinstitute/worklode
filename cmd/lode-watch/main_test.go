package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRequiresCluster(t *testing.T) {
	var out bytes.Buffer
	if code := run(context.Background(), nil, &out); code == 0 || !strings.Contains(out.String(), "cluster") {
		t.Fatalf("run without --cluster = %d, %q; want cluster error", code, out.String())
	}
}
