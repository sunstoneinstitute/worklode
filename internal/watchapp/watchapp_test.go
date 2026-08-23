package watchapp

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresServerAndToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{name: "server", want: "--server"},
		{name: "token", opts: Options{Server: "http://example.test"}, want: "--token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run(%+v) = %v, want %q error", tc.opts, err, tc.want)
			}
		})
	}
}
