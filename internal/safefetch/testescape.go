package safefetch

import "flag"

// TestEscapes names the guard relaxations a test may ask NewForTest for. Both
// zero is the production configuration; there is no way to reach either
// relaxation on a Fetcher that New built.
type TestEscapes struct {
	// Loopback permits http, a non-default port, and a loopback address,
	// because httptest serves plain http on a random loopback port.
	Loopback bool
	// AnyHost drops the host allowlist. Every address check still applies, so
	// a test that asks for it still cannot reach a blocked range.
	AnyHost bool
}

// NewForTest returns a Fetcher with the named relaxations applied. It exists
// because internal/api's tests drive the mirror path against an httptest
// origin and cannot reach an unexported field from another package; it panics
// outside a test binary so the escape is unreachable in production even if a
// call site appears. Non-test callers are also rejected at build time by
// TestEscapesAreTestOnly in rule_test.go.
func NewForTest(allowedHosts []string, maxBytes int64, e TestEscapes) *Fetcher {
	// flag.Lookup rather than testing.Testing(): importing testing from a
	// non-test file links the test flag set into the production binary. The
	// flag is registered by testing.Init before any test runs.
	return newForTest(flag.Lookup("test.v") != nil, allowedHosts, maxBytes, e)
}

// newForTest takes the under-test verdict as an argument so the refusal path
// is testable from inside a test binary, where the real check always passes.
func newForTest(underTest bool, allowedHosts []string, maxBytes int64, e TestEscapes) *Fetcher {
	if !underTest {
		panic("safefetch: NewForTest called outside a test binary")
	}
	f := New(allowedHosts, maxBytes)
	f.allowLoopback = e.Loopback
	f.allowAnyHost = e.AnyHost
	return f
}
