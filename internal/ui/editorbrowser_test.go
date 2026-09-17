//go:build narrowcheck

package ui

// editorbrowser_test.go is the WL-855 spike's evidence: it renders the document
// page with the BlockNote island on, serves it under the cockpit's real
// Content-Security-Policy, and measures two things in a headless browser —
// whether the editor mounts and stays styled under that policy, and what a real
// document body loses on the markdown round trip.
//
//	./scripts/editor-check.sh
//
// It shares the `narrowcheck` build tag with the narrow-width audit because
// that tag means one thing: this test needs a browser, and CI does not install
// one (spec 032 §12). It reuses that audit's CDP client (cdp_test.go) and its
// browser discovery (narrowbrowser_test.go).
//
// The round-trip report is the deliverable, not a pass/fail: BlockNote's
// markdown export is lossy by design and the spike's job is to say how. What
// does fail here is the editor not mounting, the export throwing, or the page
// reporting a CSP violation — the three things that would make the island
// unusable rather than merely imperfect.

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

//go:embed editorcheck.js
var editorCheckJS string

//go:embed cspwatch.js
var cspWatchJS string

//go:embed testdata/editor-roundtrip.md
var roundTripDoc string

// editorCSP is the policy internal/api's contentSecurityPolicy() builds, with
// the blob origin left off (this fixture embeds no blob). It is spelled out
// here because internal/ui must never import internal/api; if the two drift,
// the check is measuring a policy the cockpit does not serve.
const editorCSP = "default-src 'self'; img-src 'self'; media-src 'self'; script-src 'self'; " +
	"style-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; " +
	"frame-ancestors 'none'; form-action 'self'"

// editorReport is what editorcheck.js measures.
type editorReport struct {
	Violations []struct {
		Directive  string `json:"directive"`
		BlockedURI string `json:"blockedURI"`
		SourceFile string `json:"sourceFile"`
		Line       int    `json:"line"`
		Sample     string `json:"sample"`
	} `json:"violations"`
	Mounted       bool `json:"mounted"`
	InjectedStyle []struct {
		Blocked bool   `json:"blocked"`
		Bytes   int    `json:"bytes"`
		Sample  string `json:"sample"`
		Attrs   string `json:"attrs"`
	} `json:"injectedStyles"`
	BlockedStyles         int     `json:"blockedStyles"`
	AdoptedSheets         int     `json:"adoptedSheets"`
	InlineStyleAttrs      int     `json:"inlineStyleAttrs"`
	EditorPadding         string  `json:"editorPadding"`
	ProseMirrorWhiteSpace string  `json:"proseMirrorWhiteSpace"`
	MantineHiddenFromXs   string  `json:"mantineHiddenFromXs"`
	Markdown              *string `json:"markdown"`
	Error                 string  `json:"error"`
}

func TestDocEditorIslandUnderCSP(t *testing.T) {
	bin := findBrowser()
	if bin == "" {
		t.Skip("no Chrome-family browser found; set LODE_NARROW_BROWSER=/path/to/chrome to run the editor spike check")
	}
	t.Logf("browser: %s", bin)

	page, err := renderEditorPage(roundTripDoc)
	if err != nil {
		t.Fatalf("render the document page: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServerFS(Assets())))
	mux.HandleFunc("/cspwatch.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		fmt.Fprint(w, cspWatchJS)
	})
	mux.HandleFunc("/docs/EA-SPEC-2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", editorCSP)
		fmt.Fprint(w, page)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := launchBrowser(bin, t.TempDir())
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}
	defer b.close()
	session, err := b.newPage()
	if err != nil {
		t.Fatalf("open page: %v", err)
	}
	if err := b.viewport(session, 1280, 900); err != nil {
		t.Fatalf("set viewport: %v", err)
	}
	if err := b.load(session, srv.URL+"/docs/EA-SPEC-2"); err != nil {
		t.Fatalf("load the document page: %v", err)
	}
	raw, err := b.evaluate(session, editorCheckJS, true)
	if err != nil {
		t.Fatalf("run the editor check: %v", err)
	}
	var rep editorReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode the editor check report: %v (%.400s)", err, raw)
	}

	if rep.Error != "" {
		t.Errorf("the island failed: %s", rep.Error)
	}
	if !rep.Mounted {
		t.Error("no .bn-editor in the page: the React root did not mount")
	}
	if rep.Markdown == nil {
		t.Error("the editor produced no markdown: the round trip did not complete")
	}
	// The refusals themselves are expected and are not the failure — the
	// island copies every refused sheet into the CSSOM. What must hold is that
	// the rules still compute; a rule that only ships in an injected sheet is
	// the direct test of that.
	if rep.ProseMirrorWhiteSpace != "break-spaces" {
		t.Errorf("prosemirror-view's injected stylesheet did not reach the page: .ProseMirror white-space is %q, want %q", rep.ProseMirrorWhiteSpace, "break-spaces")
	}
	if rep.MantineHiddenFromXs != "none" {
		t.Errorf("Mantine's injected responsive helpers did not reach the page: .mantine-hidden-from-xs display is %q, want %q", rep.MantineHiddenFromXs, "none")
	}
	if want := nonEmptyBlocked(rep); rep.AdoptedSheets < want {
		t.Errorf("%d refused <style> element(s) carry rules but only %d were adopted into the CSSOM", want, rep.AdoptedSheets)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "\nBlockNote island under the cockpit CSP\n")
	fmt.Fprintf(&out, "  mounted: %v, .bn-editor padding-inline-start: %q\n", rep.Mounted, rep.EditorPadding)
	fmt.Fprintf(&out, "  injected-sheet probes: .ProseMirror white-space %q, .mantine-hidden-from-xs display %q\n", rep.ProseMirrorWhiteSpace, rep.MantineHiddenFromXs)
	fmt.Fprintf(&out, "  constructed stylesheets adopted: %d\n", rep.AdoptedSheets)
	fmt.Fprintf(&out, "  <style> elements in the page: %d (%d refused)\n", len(rep.InjectedStyle), rep.BlockedStyles)
	for _, st := range rep.InjectedStyle {
		fmt.Fprintf(&out, "    %s %d bytes [%s]: %s\n", map[bool]string{true: "refused", false: "applied"}[st.Blocked], st.Bytes, st.Attrs, strings.ReplaceAll(st.Sample, "\n", " "))
	}
	fmt.Fprintf(&out, "  elements carrying a style attribute: %d\n", rep.InlineStyleAttrs)
	fmt.Fprintf(&out, "  CSP violations reported: %d\n", len(rep.Violations))
	if rep.Markdown != nil {
		fmt.Fprintf(&out, "\nmarkdown round trip: %d bytes in, %d bytes out\n", len(roundTripDoc), len(*rep.Markdown))
		out.WriteString(roundTripFindings(roundTripDoc, *rep.Markdown))
		if path := os.Getenv("LODE_EDITOR_ROUNDTRIP_OUT"); path != "" {
			if err := os.WriteFile(path, []byte(*rep.Markdown), 0o644); err != nil {
				t.Errorf("write the exported markdown: %v", err)
			} else {
				fmt.Fprintf(&out, "\n  exported markdown written to %s\n", path)
			}
		}
	}
	t.Log(out.String())
}

// renderEditorPage renders the document page with the island on, then injects
// the CSP listener as the first script in the head. The injection is the test's
// own: the listener has to be registered before the bundle runs, and the page
// has no inline script to register it in.
func renderEditorPage(body string) (string, error) {
	var b strings.Builder
	v := DocView{
		Page:   PageProps{Title: "worklode: distributed-downloader-mesh", ActiveGlobal: "knowledge"},
		Doc:    model.Doc{Project: "edge-agent", Kind: "spec", Number: 2, Slug: "distributed-downloader-mesh", Title: "Distributed downloader mesh", Status: "draft", Body: body},
		Ref:    "EA-SPEC-2",
		Editor: true,
	}
	if err := Doc(v).Render(context.Background(), &b); err != nil {
		return "", err
	}
	page := b.String()
	const head = "<head>"
	i := strings.Index(page, head)
	if i < 0 {
		return "", fmt.Errorf("no <head> in the rendered page")
	}
	return page[:i+len(head)] + `<script src="/cspwatch.js"></script>` + page[i+len(head):], nil
}

// roundTripFindings reports what the export dropped, checked against the three
// constructs the spike was asked about plus the coarse shape of the document.
func roundTripFindings(in, out string) string {
	var b strings.Builder
	checks := []struct {
		what string
		has  func(string) bool
	}{
		{"YAML frontmatter (--- ... ---)", func(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "---\n") }},
		{"{#sec-N} heading anchors", func(s string) bool { return strings.Contains(s, "{#sec-") }},
		{"```mermaid fenced block", func(s string) bool { return strings.Contains(s, "```mermaid") }},
		{"a fenced code block of any kind", func(s string) bool { return strings.Contains(s, "```") }},
		{"an emphasis marker (*)", func(s string) bool { return strings.Contains(s, "*") }},
		{"a markdown link ([text](url))", func(s string) bool { return strings.Contains(s, "](") }},
	}
	for _, c := range checks {
		switch {
		case c.has(in) && c.has(out):
			fmt.Fprintf(&b, "  kept    %s\n", c.what)
		case c.has(in):
			fmt.Fprintf(&b, "  LOST    %s\n", c.what)
		default:
			fmt.Fprintf(&b, "  n/a     %s (not in the source)\n", c.what)
		}
	}
	fmt.Fprintf(&b, "  headings: %d in, %d out\n", countPrefix(in, "#"), countPrefix(out, "#"))
	return b.String()
}

func countPrefix(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// nonEmptyBlocked counts the refused <style> elements that actually carry
// rules — an empty one is a placeholder its library never filled, and nothing
// is lost by the browser refusing it.
func nonEmptyBlocked(rep editorReport) int {
	n := 0
	for _, st := range rep.InjectedStyle {
		if st.Blocked && st.Bytes > 0 {
			n++
		}
	}
	return n
}
