package ui

// contrast_test.go holds the colour and form-semantics properties the WL-233
// audit established. It is the companion to narrow_test.go: that pass measured
// the cockpit at narrow widths, this one covers what it left out, all of it
// width-independent — 1.4.3 and 1.4.11 contrast, 2.4.7 focus, and the form
// criteria (1.3.1, 4.1.2, and getting a rejected submit announced).
//
// The audit itself ran in a headless browser over every rendered page, walking
// each text node against its effective background. What survives here is the
// arithmetic that browser confirmed, run against the built stylesheet's own
// tokens — so a palette edit that drops a pair below AA fails with the
// criterion named, rather than shipping. Ratios use the WCAG 2.x relative
// luminance formula; the thresholds are 4.5:1 for text under 18.66px bold /
// 24px (which is all of the cockpit's text) and 3:1 for a control boundary.

import (
	"context"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// builtCSS returns the stylesheet that actually ships, not the Tailwind
// source: the generated file is what a browser loads.
func builtCSS(t *testing.T) string {
	t.Helper()
	f, err := Assets().Open("app.css")
	if err != nil {
		t.Fatalf("open app.css: %v", err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	return string(b)
}

// --- WCAG relative luminance ------------------------------------------------

func channel(v float64) float64 {
	v /= 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		t.Fatalf("not a six-digit hex colour: %q", hex)
	}
	var c [3]float64
	for i := range c {
		n, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			t.Fatalf("not a hex colour: %q", hex)
		}
		c[i] = channel(float64(n))
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// --- palette ----------------------------------------------------------------

var tokenRe = regexp.MustCompile(`--([a-z0-9-]+):\s*(#[0-9A-Fa-f]{6})`)

// The four rules that declare the palette. The generated stylesheet is
// pretty-printed one declaration per line, so anchoring on the newline that
// opens each rule is what keeps ":root {" from also matching the indented
// copy inside the media query and Tailwind's own ":root, :host" preamble.
const (
	blockBareLight = "\n:root {\n"
	blockMediaDark = "@media (prefers-color-scheme:dark) {\n  :root {\n"
	blockLight     = "\n:root[data-theme=\"light\"] {\n"
	blockDark      = "\n:root[data-theme=\"dark\"] {\n"
)

// themeBlock returns the custom properties declared by the rule the anchor
// opens, which must appear exactly once in the stylesheet.
func themeBlock(t *testing.T, css, anchor string) map[string]string {
	t.Helper()
	name := strings.TrimSpace(strings.ReplaceAll(anchor, "\n", " "))
	i := strings.Index(css, anchor)
	if i < 0 {
		t.Fatalf("app.css declares no %s block", name)
	}
	rest := css[i+len(anchor):]
	if strings.Contains(rest, anchor) {
		t.Fatalf("app.css declares %s more than once", name)
	}
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("%s block is unterminated", name)
	}
	out := map[string]string{}
	for _, m := range tokenRe.FindAllStringSubmatch(rest[:end], -1) {
		out[m[1]] = strings.ToUpper(m[2])
	}
	if len(out) < 20 {
		t.Fatalf("%s declared only %d colour tokens; the parser has lost the block", name, len(out))
	}
	return out
}

func themes(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	css := builtCSS(t)
	return themeBlock(t, css, blockLight), themeBlock(t, css, blockDark)
}

// TestEveryThemeIsDeclaredOnceInEachDirection guards the palette's four-way
// duplication. Each theme's values are written twice — once for the OS
// preference and once for the explicit data-theme override — and an edit that
// lands in only one copy gives a viewer a different palette depending on how
// they arrived at the theme. That is precisely how a pair the audit cleared
// would come back below AA on one path only.
func TestEveryThemeIsDeclaredOnceInEachDirection(t *testing.T) {
	css := builtCSS(t)
	bare := themeBlock(t, css, blockBareLight)
	media := themeBlock(t, css, blockMediaDark)
	light, dark := themes(t)

	for name, pair := range map[string][2]map[string]string{
		"light: :root vs :root[data-theme=\"light\"]":                   {bare, light},
		"dark: prefers-color-scheme:dark vs :root[data-theme=\"dark\"]": {media, dark},
	} {
		a, b := pair[0], pair[1]
		for k, v := range a {
			if b[k] != v {
				t.Errorf("%s: --%s is %s in one copy and %s in the other", name, k, v, b[k])
			}
		}
		for k := range b {
			if _, ok := a[k]; !ok {
				t.Errorf("%s: --%s is declared in only one copy", name, k)
			}
		}
	}
}

// textPairs are the foreground/background combinations the audit found on the
// rendered pages, each needing 4.5:1 (WCAG 1.4.3 Contrast (Minimum)) because
// no cockpit text reaches the large-text threshold. `where` names the rule or
// the page region the browser pass found the pair on, so a failure says what
// broke rather than only which tokens did.
var textPairs = []struct{ fg, bg, where string }{
	{"ink", "bg", "body text on the page background"},
	{"ink", "surface", "body text on a card"},
	{"ink", "sunk", "nav.global a.active"},
	{"ink-2", "surface", ".backlink, nav.local a"},
	{"ink-2", "sunk", ".chip.plain"},
	// --ink-3 is the muted colour, and .muted.small lands directly on the page
	// background above the first card on Work, Deliverables and Reviews — the
	// least contrasty surface it reaches, and the pair the audit found at
	// 4.25:1 on the light theme.
	{"ink-3", "bg", ".muted.small above the first card"},
	{"ink-3", "surface", ".muted inside a card, .card > .hd .meta"},
	{"ink-3", "surface-2", ".prose code, .lct .lh"},
	{"link", "surface", "a link inside a card"},
	{"link", "bg", "a link on the page background"},
	{"ev-decl", "ev-decl-bg", ".chip.declared"},
	{"ev-user", "ev-user-bg", ".chip.user"},
	{"ev-obs", "ev-obs-bg", ".chip.observed"},
	{"ev-rec", "ev-rec-bg", ".chip.recommended, .rec .rh"},
	{"ok", "ok-bg", ".chip.ok, .mode-name.ok"},
	{"warn", "warn-bg", ".chip.warn"},
	{"crit", "crit-bg", ".chip.crit, .formerr"},
	{"info", "info-bg", ".chip.info, .mode-name"},
	{"accent-ink", "accent", ".chip.lead, .btn.primary, .decision .dh"},
}

func TestTextContrastMeetsAA(t *testing.T) {
	light, dark := themes(t)
	for _, theme := range []struct {
		name string
		tok  map[string]string
	}{{"light", light}, {"dark", dark}} {
		for _, p := range textPairs {
			fg, bg := theme.tok[p.fg], theme.tok[p.bg]
			if fg == "" || bg == "" {
				t.Errorf("%s: --%s/--%s is no longer declared", theme.name, p.fg, p.bg)
				continue
			}
			if got := contrast(t, fg, bg); got < 4.5 {
				t.Errorf("%s: --%s on --%s is %.2f:1, below the 4.5:1 WCAG 1.4.3 needs (%s; %s on %s)",
					theme.name, p.fg, p.bg, got, p.where, fg, bg)
			}
		}
	}
}

// TestControlBoundaryContrastMeetsAA holds WCAG 1.4.11 Non-text Contrast for
// the one border that is load-bearing: a button, a text field or a select
// whose fill matches the surface behind it is identifiable as a control only
// by its outline, so that outline needs 3:1. --line and --line-2 are
// decorative rules and dividers and are deliberately not held to this.
func TestControlBoundaryContrastMeetsAA(t *testing.T) {
	light, dark := themes(t)
	// Every surface a .btn, .iconbtn or .fieldrow control sits on: a card
	// (--surface), the bare canvas (--bg, where the Reviews queue's decide
	// buttons are), and the two recessed fills.
	surfaces := []string{"surface", "bg", "surface-2", "sunk"}
	for _, theme := range []struct {
		name string
		tok  map[string]string
	}{{"light", light}, {"dark", dark}} {
		border := theme.tok["control-line"]
		if border == "" {
			t.Fatalf("%s: --control-line is no longer declared", theme.name)
		}
		for _, s := range surfaces {
			if got := contrast(t, border, theme.tok[s]); got < 3 {
				t.Errorf("%s: --control-line on --%s is %.2f:1, below the 3:1 WCAG 1.4.11 needs for a control's boundary",
					theme.name, s, got)
			}
		}
	}
}

// TestPrimaryButtonBoundaryContrastMeetsAA holds the same 1.4.11 rule for
// .btn.primary, which does not use --control-line: it is filled with --accent
// and outlined with --accent-line, so both of those are what identifies it
// against the surface behind it. Either one clearing 3:1 would do — a filled
// control is bounded by its own fill — but holding both is what keeps a later
// palette edit from moving the boundary onto the token that lost it. Held
// against the same four surfaces as --control-line, because --accent is a fill
// elsewhere too (.chip.lead, .decision .dh, .step.current .pip).
//
// The light theme failed this until WL-260 darkened the accent: #FAD604 was
// 1.43:1 on --surface and #E4C000 1.77:1, which is why spec 032 §10 carried it
// as an open exception.
func TestPrimaryButtonBoundaryContrastMeetsAA(t *testing.T) {
	light, dark := themes(t)
	surfaces := []string{"surface", "bg", "surface-2", "sunk"}
	for _, theme := range []struct {
		name string
		tok  map[string]string
	}{{"light", light}, {"dark", dark}} {
		for _, tok := range []string{"accent", "accent-line"} {
			c := theme.tok[tok]
			if c == "" {
				t.Fatalf("%s: --%s is no longer declared", theme.name, tok)
			}
			for _, s := range surfaces {
				if got := contrast(t, c, theme.tok[s]); got < 3 {
					t.Errorf("%s: .btn.primary's --%s on --%s is %.2f:1, below the 3:1 WCAG 1.4.11 needs for a control's boundary",
						theme.name, tok, s, got)
				}
			}
		}
	}
}

// TestPrimaryButtonIsDrawnWithTheAccentTokens keeps the arithmetic above
// pointed at the rule it is about: .btn.primary reverting to any other fill or
// border would pass every check here while failing 1.4.11 on the page.
func TestPrimaryButtonIsDrawnWithTheAccentTokens(t *testing.T) {
	flat := strings.Join(strings.Fields(builtCSS(t)), "")
	const want = ".btn.primary{background:var(--accent);border-color:var(--accent-line);color:var(--accent-ink)"
	if !strings.Contains(flat, want) {
		t.Errorf("the primary button is no longer drawn with the accent tokens: %q (WCAG 1.4.11)", want)
	}
}

// TestFocusIndicatorContrastMeetsAA holds WCAG 2.4.7 with 1.4.11: the shell's
// one :focus-visible rule draws a --link outline at outline-offset:2px, so the
// colour it must stand out from is the surface behind the focused element, not
// the element's own fill.
func TestFocusIndicatorContrastMeetsAA(t *testing.T) {
	if css := builtCSS(t); !strings.Contains(strings.Join(strings.Fields(css), ""), ":focus-visible{outline:2pxsolidvar(--link);outline-offset:2px") {
		t.Error("the shell's single :focus-visible outline rule is gone; every control's focus indicator depended on it (WCAG 2.4.7)")
	}
	light, dark := themes(t)
	for _, theme := range []struct {
		name string
		tok  map[string]string
	}{{"light", light}, {"dark", dark}} {
		for _, s := range []string{"surface", "bg", "surface-2", "sunk"} {
			if got := contrast(t, theme.tok["link"], theme.tok[s]); got < 3 {
				t.Errorf("%s: the focus outline (--link) on --%s is %.2f:1, below the 3:1 a focus indicator needs (WCAG 2.4.7/1.4.11)",
					theme.name, s, got)
			}
		}
	}
}

// TestControlsOutlineWithTheControlToken keeps the arithmetic above pointed at
// the borders it is about. --line-2 is a third of the contrast --control-line
// carries, so a control that reverts to it passes every test here while
// failing 1.4.11 on the page.
func TestControlsOutlineWithTheControlToken(t *testing.T) {
	flat := strings.Join(strings.Fields(builtCSS(t)), "")
	for _, want := range []string{
		".btn{border:1pxsolidvar(--control-line)",
		".iconbtn{width:34px;height:34px;border-radius:8px;border:1pxsolidvar(--control-line)",
		"border:1pxsolidvar(--control-line);border-radius:9px;padding:9px11px;min-height:40px;width:100%",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("a control's boundary no longer uses --control-line: %q (WCAG 1.4.11)", want)
		}
	}
}

// --- the two creation forms -------------------------------------------------

// formPages renders both creation forms in both states — first render and
// rejected submit — because the accessible behaviour of a rejected submit is
// the half that had nothing holding it.
func formPages(t *testing.T) map[string]string {
	t.Helper()
	proj := CockpitProject{ID: "p", Name: "Project", Key: "P"}
	shell := func(errMsg string) FormShell {
		return FormShell{Page: PageProps{Title: "t"}, Project: proj, Action: "/a", CancelURL: "/c", Error: errMsg}
	}
	opts := []FormOption{{Value: "high", Label: "High", Selected: true}}
	comps := map[string]templ.Component{
		"newtask":           NewTask(NewTaskView{Form: shell(""), Priorities: opts, Kinds: opts, Concerns: opts}),
		"newtask/error":     NewTask(NewTaskView{Form: shell("A title is required."), Priorities: opts, Kinds: opts, Concerns: opts}),
		"deliverable":       NewDeliverable(NewDeliverableView{Form: shell("")}),
		"deliverable/error": NewDeliverable(NewDeliverableView{Form: shell("A name is required.")}),
	}
	out := make(map[string]string, len(comps))
	for name, c := range comps {
		var b strings.Builder
		if err := c.Render(context.Background(), &b); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out[name] = b.String()
	}
	return out
}

var (
	controlRe = regexp.MustCompile(`<(input|select|textarea)\b[^>]*>`)
	attrRe    = regexp.MustCompile(`\b([a-z-]+)="([^"]*)"`)
)

func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestEveryFormControlIsLabelled holds WCAG 4.1.2 Name, Role, Value: a control
// a screen reader reaches with no accessible name is an unnamed edit box. The
// forms label every control with <label for>, so the check is that each
// control's id is the target of exactly one such label.
func TestEveryFormControlIsLabelled(t *testing.T) {
	for name, body := range formPages(t) {
		labelled := map[string]int{}
		for _, m := range regexp.MustCompile(`<label for="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			labelled[m[1]]++
		}
		seen := 0
		for _, tag := range controlRe.FindAllString(body, -1) {
			a := attrs(tag)
			if a["type"] == "hidden" || a["type"] == "submit" {
				continue
			}
			seen++
			switch {
			case a["id"] == "":
				t.Errorf("%s: a form control has no id, so no <label for> can name it: %s (WCAG 4.1.2)", name, tag)
			case labelled[a["id"]] == 0:
				t.Errorf("%s: control %q has no <label for=%q> (WCAG 4.1.2)", name, a["id"], a["id"])
			case labelled[a["id"]] > 1:
				t.Errorf("%s: control %q is named by %d labels (WCAG 4.1.2)", name, a["id"], labelled[a["id"]])
			}
		}
		if seen < 3 {
			t.Errorf("%s: found only %d controls; the fixture is not rendering the form", name, seen)
		}
	}
}

// TestFieldHintIsAssociatedWithItsField holds WCAG 1.3.1 Info and
// Relationships. The deliverable URL field's note explains what the field is
// for, but it sat beside the input as a plain <span>: a screen reader arriving
// on the input announced the label and nothing else. aria-describedby is the
// relationship that was only visual.
func TestFieldHintIsAssociatedWithItsField(t *testing.T) {
	for name, body := range formPages(t) {
		for _, m := range regexp.MustCompile(`<span id="([^"]+)" class="hint"`).FindAllStringSubmatch(body, -1) {
			if !strings.Contains(body, `aria-describedby="`+m[1]+`"`) {
				t.Errorf("%s: the hint %q describes no field (WCAG 1.3.1)", name, m[1])
			}
		}
		for _, m := range regexp.MustCompile(`aria-describedby="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			if !strings.Contains(body, `id="`+m[1]+`"`) {
				t.Errorf("%s: aria-describedby points at %q, which nothing declares (WCAG 1.3.1)", name, m[1])
			}
		}
		// A .hint that never gained an id would pass the first loop vacuously.
		if strings.Contains(body, `class="hint"`) && !strings.Contains(body, `<span id="url-hint" class="hint"`) {
			t.Errorf("%s: a .hint renders without an id, so it can describe nothing (WCAG 1.3.1)", name)
		}
	}
}

// TestRejectedSubmitAnnouncesItself holds what happens to a screen-reader user
// who submits an invalid form. The message used to sit in an aria-live region,
// which announces nothing here: a live region fires for a change to a document
// already on screen, and a rejected submit is a whole new document, so the
// user landed at the top of a reloaded page with no signal that anything had
// been rejected. Moving focus to the message is what reaches them.
func TestRejectedSubmitAnnouncesItself(t *testing.T) {
	pages := formPages(t)
	for name, body := range pages {
		rejected := strings.HasSuffix(name, "/error")
		if got := strings.Contains(body, `class="formerr"`); got != rejected {
			t.Errorf("%s: .formerr rendered=%v, want %v", name, got, rejected)
		}
		if strings.Contains(body, "aria-live") {
			t.Errorf("%s: the form carries an aria-live region, which announces nothing across a page load — move focus instead", name)
		}
		if !rejected {
			continue
		}
		if !strings.Contains(body, `<p id="form-error" class="formerr" tabindex="-1" autofocus>`) {
			t.Errorf("%s: the validation message must take focus on load, or the rejection is never announced", name)
		}
		// Two autofocus elements leave which one wins to the browser; the
		// field's is conditional so the message's is the only one on a
		// rejected submit.
		if n := strings.Count(body, "autofocus"); n != 1 {
			t.Errorf("%s: %d elements carry autofocus, want exactly 1 (the message)", name, n)
		}
	}
	// The first field keeps its autofocus on a clean render — the message is
	// only meant to take it away when there is a message.
	if !strings.Contains(pages["newtask"], `id="title"`) || !strings.Contains(pages["newtask"], "autofocus") {
		t.Error("newtask: the title field should still autofocus on a first render")
	}
}
