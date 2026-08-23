package serverapp

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresDSN(t *testing.T) {
	err := Run(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "no DSN") {
		t.Fatalf("Run without a DSN = %v, want no DSN error", err)
	}
}
