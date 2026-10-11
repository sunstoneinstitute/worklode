package api

import (
	"strings"
	"testing"
)

// server_test.go is package api_test and cannot reach the unexported
// validateLegacyURL/Config fields this test exercises, so it lives here
// instead, alongside the other *_internal_test.go files.
func TestLegacyURLValidation(t *testing.T) {
	for _, tc := range []struct{ public, legacy, wantErr string }{
		{"", "https://worklold.test", "needs LODE_PUBLIC_URL"},
		{"https://worklode.test", "worklold.test", "absolute http(s) URL"},
		{"https://worklode.test", "https://worklode.test:8443", "different host"},
		{"https://worklode.test", "https://worklold.test", ""},
	} {
		err := validateLegacyURL(Config{PublicURL: tc.public, LegacyURL: tc.legacy})
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%+v: err = %v, want %q", tc, err, tc.wantErr)
		}
	}
}
