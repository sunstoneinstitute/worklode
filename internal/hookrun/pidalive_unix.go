//go:build unix

package hookrun

import "syscall"

// pidAlive reports whether pid names a live process (signal 0 probe). Any
// error — ESRCH in particular — is treated as dead.
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
