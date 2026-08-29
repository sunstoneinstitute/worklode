package store

import "testing"

// WL-352: the admin connection is one process-wide pool, not a fresh
// open+ping+close per test — that handshake was ~8-26ms of pure waste on
// each of the ~1200 OpenTestStore calls across the suite.
func TestAdminConnIsSharedAcrossTests(t *testing.T) {
	t.Parallel()
	first := adminConnForTest(t)

	// A subtest whose cleanup runs before we use the pool again: the shared
	// pool must survive it (the old helper closed its pool in t.Cleanup).
	t.Run("inner", func(t *testing.T) {
		if got := adminConnForTest(t); got != first {
			t.Fatalf("adminConnForTest returned a different pool inside a subtest")
		}
	})

	if got := adminConnForTest(t); got != first {
		t.Fatalf("adminConnForTest returned a different pool on a second call")
	}
	if err := first.Ping(); err != nil {
		t.Fatalf("the shared admin pool was closed by a test cleanup: %v", err)
	}
}
