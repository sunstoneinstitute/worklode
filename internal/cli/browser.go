// browser.go decides whether this machine can launch a browser, and how.
// `lode login` needs the answer up front: when no browser can be opened it
// falls back to manual mode (spec 001 §8.7) instead of failing.
package cli

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// ErrNoBrowser reports that no browser can be launched here. RunLogin treats it
// as the signal to switch to manual mode, so an OpenBrowser injected by a caller
// can request that fallback by returning it. Every other launch error stays
// fatal: it means a browser may well have opened, and falling back anyway would
// leave two logins racing for the same terminal.
var ErrNoBrowser = errors.New("no browser available on this machine")

// openBrowser opens url in the platform default browser.
func openBrowser(url string) error {
	name, args, err := browserCommand(runtime.GOOS, url, exec.LookPath, os.Getenv)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

// browserCommand returns the command that opens url on goos, or ErrNoBrowser
// when this machine has no way to open one. lookPath and getenv are injected so
// the decision is testable on any host.
//
// The display check is the load-bearing half. A missing opener binary is caught
// by lookPath, but on a Linux server xdg-open is usually installed and simply
// has no display to open onto: it fails long after exec.Start has reported
// success, so only the absent DISPLAY/WAYLAND_DISPLAY tells us in time.
func browserCommand(goos, url string, lookPath func(string) (string, error), getenv func(string) string) (string, []string, error) {
	var name string
	var args []string
	switch goos {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return "", nil, ErrNoBrowser
		}
		name, args = "xdg-open", []string{url}
	}
	if _, err := lookPath(name); err != nil {
		return "", nil, ErrNoBrowser
	}
	return name, args, nil
}
