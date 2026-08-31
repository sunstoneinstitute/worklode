package ui

// layout_test.go covers the top bar's inbox indicator (spec 056 §4): the
// icon renders on every page, and the alert dot renders only when the
// request context carries WithInboxDot(ctx, true).

import (
	"context"
	"strings"
	"testing"
)

// TestInboxIconRendersOnEveryPage reuses narrow_test.go's pages() fixture —
// one rendered instance of every page component this package exports,
// already exercised through the app shell — to pin that the inbox icon
// reaches every page without a dot when the context carries no flag (the
// unset case pages() renders, matching a request renderWeb never touched,
// e.g. an unauthenticated one).
func TestInboxIconRendersOnEveryPage(t *testing.T) {
	for name, body := range pages(t) {
		if !strings.Contains(body, `href="/inbox"`) || !strings.Contains(body, `aria-label="Inbox"`) {
			t.Errorf("%s: missing the inbox icon", name)
		}
		if strings.Contains(body, "dot-alert") {
			t.Errorf("%s: rendered the inbox dot with no flag set", name)
		}
	}
}

// TestInboxDotFollowsContextFlag pins the dot to WithInboxDot's value: present
// only when true, absent when false or unset.
func TestInboxDotFollowsContextFlag(t *testing.T) {
	render := func(ctx context.Context) string {
		t.Helper()
		var b strings.Builder
		if err := Home(HomeView{Page: PageProps{Title: "Home"}}).Render(ctx, &b); err != nil {
			t.Fatalf("render Home: %v", err)
		}
		return b.String()
	}

	if body := render(WithInboxDot(context.Background(), true)); !strings.Contains(body, `class="dot dot-alert"`) {
		t.Errorf("WithInboxDot(true): no dot rendered\n%s", body)
	}
	if body := render(WithInboxDot(context.Background(), false)); strings.Contains(body, "dot-alert") {
		t.Errorf("WithInboxDot(false): dot rendered anyway\n%s", body)
	}
	if body := render(context.Background()); strings.Contains(body, "dot-alert") {
		t.Errorf("no flag set: dot rendered anyway\n%s", body)
	}
}
