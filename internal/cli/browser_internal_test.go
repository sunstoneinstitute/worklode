package cli

import (
	"errors"
	"os/exec"
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	missing := func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	x11 := map[string]string{"DISPLAY": ":0"}
	wayland := map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
	headless := map[string]string{}

	tests := []struct {
		name     string
		goos     string
		lookPath func(string) (string, error)
		env      map[string]string
		wantCmd  string
		wantErr  bool
	}{
		{"linux with X11", "linux", found, x11, "xdg-open", false},
		{"linux under wayland", "linux", found, wayland, "xdg-open", false},
		// The case this feature exists for: xdg-open is installed on the server,
		// but over SSH it has no display to open onto. Start() would report
		// success and the login would then wait for a callback that can never
		// arrive, so the absence of a display has to be caught up front.
		{"linux over ssh, no display", "linux", found, headless, "", true},
		{"linux without xdg-open", "linux", missing, x11, "", true},
		{"freebsd with X11", "freebsd", found, x11, "xdg-open", false},
		// macOS and Windows have no DISPLAY and need none.
		{"darwin", "darwin", found, headless, "open", false},
		{"darwin without open", "darwin", missing, headless, "", true},
		{"windows", "windows", found, headless, "rundll32", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args, err := browserCommand(tt.goos, "https://lode.example/x", tt.lookPath, env(tt.env))
			if tt.wantErr {
				if !errors.Is(err, ErrNoBrowser) {
					t.Fatalf("err = %v; want ErrNoBrowser", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("browserCommand: %v", err)
			}
			if name != tt.wantCmd {
				t.Errorf("command = %q; want %q", name, tt.wantCmd)
			}
			if len(args) == 0 || args[len(args)-1] != "https://lode.example/x" {
				t.Errorf("args = %q; want the URL last", args)
			}
		})
	}
}
