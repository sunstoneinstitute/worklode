package cmd

import (
	"maps"
	"strings"
	"testing"
)

// TestParseClusterEnvMap: only dev and prod are accepted. env_deploys holds
// no other stage, so accepting one would give a server that records
// deployments but never advances a task.
func TestParseClusterEnvMap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    map[string]string
		wantErr string
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "dev and prod",
			in:   "hzdev=dev, hzprod=prod,admin=prod",
			want: map[string]string{"hzdev": "dev", "hzprod": "prod", "admin": "prod"},
		},
		{name: "entry without =", in: "hzdev", want: map[string]string{}},
		{name: "unknown value", in: "staging-1=staging", wantErr: `cluster "staging-1" maps to "staging"`},
		{name: "empty value", in: "hzdev=", wantErr: `cluster "hzdev" maps to ""`},
		{name: "one bad among good", in: "hzdev=dev,qa=qa", wantErr: `cluster "qa" maps to "qa"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseClusterEnvMap(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseClusterEnvMap(%q) = %v, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), "LODE_CLUSTER_ENV_MAP") {
					t.Fatalf("error = %q, want it to name LODE_CLUSTER_ENV_MAP", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseClusterEnvMap(%q): %v", tc.in, err)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("parseClusterEnvMap(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
