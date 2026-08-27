package storederive

import "time"

// SetClock overrides the clock GitHubReader's client cache stamps and checks
// entries against, so a test can step past repoClientTTL without sleeping.
// Test-only: declared in a _test.go file, so it is not part of the package's
// API.
func (g *GitHubReader) SetClock(f func() time.Time) { g.now = f }

// RepoClientTTL exposes the cache's freshness window to the external test
// package.
const RepoClientTTL = repoClientTTL
