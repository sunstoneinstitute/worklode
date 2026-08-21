package ui

// csp_test.go holds what internal/api's `style-src 'self'` depends on (WL-227).
// A CSP directive is only as tight as the markup lets it be: one style
// attribute anywhere in this package, or one <style> element htmx injects, and
// the directive has to take 'unsafe-inline' back. Both facts live here, in the
// package that would break them, rather than in the package that sets the
// header.

import (
	"encoding/json"
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
