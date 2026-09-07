package api

import "testing"

// TestDecideReturn: the decide form's return field is attacker-controlled, so
// only a same-site absolute path may be redirected to. Everything else falls
// back to the queue rather than sending a signed-in reviewer off-site.
func TestDecideReturn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"", "/reviews"},
		{"/docs/WL-SPEC-25", "/docs/WL-SPEC-25"},
		{"/docs/WL-SPEC-25?body=source", "/docs/WL-SPEC-25?body=source"},
		{"//evil.example.com/", "/reviews"},
		{"https://evil.example.com/", "/reviews"},
		{"docs/WL-SPEC-25", "/reviews"},
		{"javascript:alert(1)", "/reviews"},
	} {
		if got := decideReturn(c.in); got != c.want {
			t.Errorf("decideReturn(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
