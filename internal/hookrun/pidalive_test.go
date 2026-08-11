package hookrun

import (
	"os"
	"testing"
)

func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Fatal("pidAlive(self) = false, want true")
	}
}

func TestPidAliveDead(t *testing.T) {
	// A pid far above any live process; the probe must read it as dead.
	if pidAlive(1 << 30) {
		t.Fatal("pidAlive(1<<30) = true, want false")
	}
}
