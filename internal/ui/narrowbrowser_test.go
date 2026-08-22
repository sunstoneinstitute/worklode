//go:build narrowcheck

package ui

// narrowbrowser_test.go re-runs the WL-140 narrow-width audit: every page
// component this package renders, in a headless browser, at the four viewports
// the audit used, measuring horizontal overflow (WCAG 1.4.10), clipped text
// (1.4.10), pointer-target size (2.5.8) and focus obscuring (2.4.11).
//
//	./scripts/narrow-check.sh
//
// It is behind the `narrowcheck` build tag because it needs a browser, and CI
// has no business installing one (spec 032 §12). narrow_test.go — untagged, and
// the thing `make test` runs — pins each fix the audit produced as a markup or
// stylesheet fact. That guards a rule from being deleted; this guards a *new*
// page from arriving broken, which is the thing a static assertion cannot see.
// Both read the same fixtures (narrow_test.go's `pages`), so a new page becomes
// measured by being added there once.
//
// A finding is a real measurement, not a verdict handed down: read what it
// says, decide whether it is a reflow bug or a deliberate exception, and fix
// the page or record why not.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

//go:embed narrowcheck.js
var narrowCheckJS string

// narrowViewports are the four widths the WL-140 audit measured: two phones,
// a 1280px desktop at 200% zoom (which is what 1.4.10 actually asks for), and
// a tablet.
var narrowViewports = []struct {
	Name          string
	Width, Height int
}{
	{"320x640 small phone", 320, 640},
	{"375x667 phone", 375, 667},
	{"640x800 1280px desktop at 200% zoom", 640, 800},
	{"768x1024 tablet", 768, 1024},
}

// narrowReport is what narrowcheck.js measures for one page at one viewport.
type narrowReport struct {
	Viewport     int `json:"viewport"`
	PageWidth    int `json:"pageWidth"`
	StickyBottom int `json:"stickyBottom"`
	Overflow     []struct {
		Sel   string `json:"sel"`
		Right int    `json:"right"`
		Width int    `json:"width"`
	} `json:"overflow"`
	Truncated []struct {
		Sel    string `json:"sel"`
		Shown  int    `json:"shown"`
		Needed int    `json:"needed"`
		Text   string `json:"text"`
	} `json:"truncated"`
	Targets []struct {
		Sel    string  `json:"sel"`
		W      float64 `json:"w"`
		H      float64 `json:"h"`
		Inline bool    `json:"inline"`
	} `json:"targets"`
	Focus []struct {
		Sel  string `json:"sel"`
		By   string `json:"by"`
		Kind string `json:"kind"`
	} `json:"focus"`
}

// browserCandidates are the Chrome-family binaries to try, in order: whatever
// LODE_NARROW_BROWSER names, then the usual names on PATH, then the places a
// browser tends to already be on a developer's machine.
func browserCandidates() []string {
	var out []string
	if bin := os.Getenv("LODE_NARROW_BROWSER"); bin != "" {
		out = append(out, bin)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			out = append(out, path)
		}
	}
	out = append(out,
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	)
	// Playwright and Puppeteer both cache a Chromium; either will do.
	if home, err := os.UserHomeDir(); err == nil {
		for _, pattern := range []string{
			filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux*/chrome"),
			filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-mac*/Chromium.app/Contents/MacOS/Chromium"),
			filepath.Join(home, ".cache/puppeteer/chrome/*/chrome-linux*/chrome"),
		} {
			matches, _ := filepath.Glob(pattern)
			sort.Strings(matches)
			out = append(out, matches...)
		}
	}
	return out
}

// findBrowser returns the first candidate that exists and is executable, or ""
// when the machine has none.
func findBrowser() string {
	for _, path := range browserCandidates() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// serveFixtures publishes the rendered pages at /pages/<name> and this
// package's real assets at /assets/, so the browser loads the same stylesheet,
// fonts and scripts a running cockpit serves. Rendering to files and opening
// them over file:// would not work: every page references /assets/app.css by
// absolute path.
func serveFixtures(t *testing.T, rendered map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServerFS(Assets())))
	mux.HandleFunc("/pages/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := rendered[strings.TrimPrefix(r.URL.Path, "/pages/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNarrowWidthAudit(t *testing.T) {
	bin := findBrowser()
	if bin == "" {
		t.Skip("no Chrome-family browser found; set LODE_NARROW_BROWSER=/path/to/chrome to run the narrow-width audit")
	}
	t.Logf("browser: %s", bin)

	rendered := pages(t)
	names := make([]string, 0, len(rendered))
	for name := range rendered {
		names = append(names, name)
	}
	sort.Strings(names)

	srv := serveFixtures(t, rendered)

	b, err := launchBrowser(bin, t.TempDir())
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}
	defer b.close()
	session, err := b.newPage()
	if err != nil {
		t.Fatalf("open page: %v", err)
	}

	var out strings.Builder
	findings := 0
	// Notes are informational and mostly identical at every viewport, so they
	// are collected once and printed after the per-viewport findings.
	notes := map[string]bool{}
	fmt.Fprintf(&out, "\nnarrow-width audit: %d pages x %d viewports\n", len(names), len(narrowViewports))

	for _, vp := range narrowViewports {
		if err := b.viewport(session, vp.Width, vp.Height); err != nil {
			t.Fatalf("set viewport %s: %v", vp.Name, err)
		}
		clean := true
		var section strings.Builder
		for _, name := range names {
			url := srv.URL + "/pages/" + name
			if err := b.load(session, url); err != nil {
				t.Fatalf("%s at %s: %v", name, vp.Name, err)
			}
			raw, err := b.evaluate(session, narrowCheckJS, false)
			if err != nil {
				t.Fatalf("%s at %s: %v", name, vp.Name, err)
			}
			var rep narrowReport
			if err := json.Unmarshal([]byte(raw), &rep); err != nil {
				t.Fatalf("%s at %s: decode report: %v (%.200s)", name, vp.Name, err, raw)
			}
			before := section.Len()
			findings += writeFindings(&section, name, rep, notes)
			if section.Len() > before {
				clean = false
			}
		}
		fmt.Fprintf(&out, "\n  %s\n", vp.Name)
		if clean {
			fmt.Fprintf(&out, "    no findings\n")
			continue
		}
		out.WriteString(section.String())
	}

	if len(notes) > 0 {
		fmt.Fprintf(&out, "\n  notes (not counted as findings)\n")
		lines := make([]string, 0, len(notes))
		for note := range notes {
			lines = append(lines, note)
		}
		sort.Strings(lines)
		for _, line := range lines {
			fmt.Fprintf(&out, "    %s\n", line)
		}
	}
	fmt.Fprintf(&out, "\n%d finding(s)\n", findings)
	t.Log(out.String())
	if findings > 0 {
		t.Errorf("the narrow-width audit found %d issue(s); see the report above", findings)
	}
}

// writeFindings appends one page's findings to w and returns how many it wrote.
// A pointer target that is an inline link in flowing text carries 2.5.8's own
// inline exception, so it goes into notes — printed once for the whole run —
// rather than being counted.
func writeFindings(w *strings.Builder, name string, rep narrowReport, notes map[string]bool) int {
	var lines []string
	if len(rep.Overflow) > 0 {
		lines = append(lines, fmt.Sprintf("1.4.10 Reflow: the page is %dpx wide in a %dpx viewport", rep.PageWidth, rep.Viewport))
		for _, o := range rep.Overflow {
			lines = append(lines, fmt.Sprintf("  %s reaches %dpx (%dpx wide)", o.Sel, o.Right, o.Width))
		}
	}
	for _, tr := range rep.Truncated {
		lines = append(lines, fmt.Sprintf("1.4.10 Reflow: %s clips its text to %dpx of %dpx (%q)", tr.Sel, tr.Shown, tr.Needed, tr.Text))
	}
	for _, tg := range rep.Targets {
		msg := fmt.Sprintf("2.5.8 Target Size: %s %s is %gx%gpx, under 24x24", name, tg.Sel, tg.W, tg.H)
		if tg.Inline {
			notes[msg+" (inline link — the criterion's inline exception applies)"] = true
			continue
		}
		lines = append(lines, msg)
	}
	for _, f := range rep.Focus {
		what := "focused control"
		if f.Kind == "jump" {
			what = "in-page jump target"
		}
		lines = append(lines, fmt.Sprintf("2.4.11 Focus Not Obscured: %s %s is covered by %s", what, f.Sel, f.By))
	}
	if len(lines) == 0 {
		return 0
	}
	fmt.Fprintf(w, "    %s\n", name)
	for _, l := range lines {
		fmt.Fprintf(w, "      %s\n", l)
	}
	return len(lines)
}
