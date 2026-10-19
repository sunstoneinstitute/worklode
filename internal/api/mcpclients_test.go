package api

import "testing"

func TestMCPClientRedirect(t *testing.T) {
	const local = "an app on this computer (localhost:33418)"
	const claude = "the Worklode connector in claude.ai"
	for _, tc := range []struct {
		name, client, uri, want string
		ok                      bool
	}{
		{"localhost", "worklode-mcp", "http://localhost:33418/callback", local, true},
		{"loopback ip", "worklode-mcp", "http://127.0.0.1:33418/callback", local, true},
		{"claude.ai", "worklode-mcp", "https://claude.ai/api/mcp/auth_callback", claude, true},
		{"unknown client", "other", "http://localhost:33418/callback", "", false},
		{"wrong port", "worklode-mcp", "http://localhost:33419/callback", "", false},
		{"trailing slash", "worklode-mcp", "http://localhost:33418/callback/", "", false},
		{"http claude.ai", "worklode-mcp", "http://claude.ai/api/mcp/auth_callback", "", false},
		{"empty", "worklode-mcp", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := clientRedirect(tc.client, tc.uri)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("clientRedirect(%q, %q) = %q, %v", tc.client, tc.uri, got, ok)
			}
		})
	}
}
