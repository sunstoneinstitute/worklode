package projector

import "time"

// Metrics holds the projector loop's instruments (spec 022). This is a
// placeholder: Task 4 replaces its body with real Prometheus instruments.
// Every recording method is a nil-safe no-op for now, so RunOnce can call
// them unconditionally on a nil *Metrics — as the tests in this package do.
type Metrics struct{}

// recordRun records the outcome and duration of one RunOnce call.
func (m *Metrics) recordRun(result string, d time.Duration) {}

// recordProjects records how many project graphs one successful RunOnce
// call wrote.
func (m *Metrics) recordProjects(n int) {}
