package ui

// csp_test.go holds what internal/api's `style-src 'self'` depends on (WL-227).
// A CSP directive is only as tight as the markup lets it be: one style
// attribute anywhere in this package, or one <style> element htmx injects, and
// the directive has to take 'unsafe-inline' back. Both facts live here, in the
// package that would break them, rather than in the package that sets the
// header.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPageCarriesAnInlineStyle holds the markup half of `style-src 'self'`.
// The cockpit's mode chip used to colour itself with a style attribute; it now
// carries a .mode-name.ok class instead. Any page reintroducing an inline
// style — attribute or element — is invisible under the policy, so fail here
// rather than in a browser.
func TestNoPageCarriesAnInlineStyle(t *testing.T) {
	for name, body := range pages(t) {
		if i := strings.Index(body, " style="); i >= 0 {
			t.Errorf("%s: inline style attribute at %q — style-src 'self' would drop it; use a class in app.tailwind.css", name, excerpt(body, i))
		}
		if i := strings.Index(body, "<style"); i >= 0 {
			t.Errorf("%s: inline <style> element at %q — style-src 'self' would drop it", name, excerpt(body, i))
		}
	}
}

// TestNoUISourceCarriesAnInlineStyle is the same rule read off the source
// rather than off a rendered page, because a page is only rendered here once
// it has a fixture: the Progress page shipped a style attribute on every bar
// slice (WL-769) and TestNoPageCarriesAnInlineStyle never saw it. Scanning
// every .templ and its generated .go covers the pages no fixture reaches.
func TestNoUISourceCarriesAnInlineStyle(t *testing.T) {
	names, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatal(err)
	}
	gen, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range gen {
		if !strings.HasSuffix(n, "_test.go") {
			names = append(names, n)
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			// A comment line is prose about the rule, not markup under it —
			// this file's own doc comments name both spellings.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, bad := range []string{"style=", "<style"} {
				if i := strings.Index(line, bad); i >= 0 {
					t.Errorf("%s:%d: %q at %q — the cockpit is served under style-src 'self' with no "+
						"nonce, so a style attribute or <style> element is dropped by the browser and "+
						"the page renders wrong; put the rule in internal/ui/styles/app.tailwind.css "+
						"and name a class", name, n+1, bad, excerpt(line, i))
				}
			}
		}
	}
}

// TestEveryPageDisablesHtmxIndicatorStyles holds the other half. htmx's
// includeIndicatorStyles defaults to true, and it injects its <style> element
// before any page code runs — so the only place to turn it off without a
// nonce is the config meta, and it has to be on every page, since the script
// tag is in the shared layout.
func TestEveryPageDisablesHtmxIndicatorStyles(t *testing.T) {
	var cfg struct {
		IncludeIndicatorStyles *bool `json:"includeIndicatorStyles"`
	}
	if err := json.Unmarshal([]byte(htmxConfig), &cfg); err != nil {
		t.Fatalf("htmxConfig is not the JSON htmx parses: %v", err)
	}
	if cfg.IncludeIndicatorStyles == nil || *cfg.IncludeIndicatorStyles {
		t.Fatalf("htmxConfig must set includeIndicatorStyles false, got %s", htmxConfig)
	}

	// The rendered attribute is HTML-escaped; what matters is that the meta
	// is present and names the setting, on every page.
	for name, body := range pages(t) {
		meta := strings.Index(body, `<meta name="htmx-config"`)
		if meta < 0 {
			t.Errorf("%s: no htmx-config meta — htmx would inject an unnonced <style>", name)
			continue
		}
		end := strings.Index(body[meta:], ">")
		if end < 0 || !strings.Contains(body[meta:meta+end], "includeIndicatorStyles") {
			t.Errorf("%s: htmx-config meta does not name includeIndicatorStyles: %q", name, excerpt(body, meta))
		}
		if script := strings.Index(body, "/assets/htmx.min.js"); script >= 0 && script < meta {
			t.Errorf("%s: htmx loads before its config meta; the meta must be in <head>", name)
		}
	}
}

func excerpt(body string, at int) string {
	end := at + 80
	if end > len(body) {
		end = len(body)
	}
	return body[at:end]
}
