//go:build windows

package hookrun

import "golang.org/x/sys/windows"

// pidAlive reports whether pid names a live process. It opens the process for a
// limited-information query and reads its exit code: a still-running process
// reports STILL_ACTIVE (259). Any error — the process not existing in
// particular — is treated as dead, matching the Unix signal-0 probe.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
