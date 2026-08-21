package projector

import "time"

// SetClock overrides the projector's clock so a test can step past a
// quarantined project's backoff without sleeping. Test-only: declared in a
// _test.go file, so it is not part of the package's API.
func (p *Projector) SetClock(f func() time.Time) { p.clock = f }

// RetryDelay exposes the backoff curve to the external test package.
func RetryDelay(attempts int) time.Duration { return retryDelay(attempts) }
