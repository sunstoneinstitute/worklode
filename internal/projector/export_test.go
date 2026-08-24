package projector

import "time"

// SetClock overrides the projector's clock so a test can step past a
// quarantined project's backoff without sleeping. Test-only: declared in a
// _test.go file, so it is not part of the package's API.
func (p *Projector) SetClock(f func() time.Time) { p.clock = f }

// RetryDelay exposes the backoff curve to the external test package.
func RetryDelay(attempts int) time.Duration { return retryDelay(attempts) }

// SetJitter pins the retry spread so a test can assert an exact
// next_attempt_at. Test-only, like SetClock.
func (p *Projector) SetJitter(f func() float64) { p.rand = f }

// JitteredDelay exposes the spread backoff to the external test package.
func JitteredDelay(attempts int, r float64) time.Duration { return jitteredDelay(attempts, r) }
