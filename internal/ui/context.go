package ui

// context.go carries the inbox indicator's has-items flag (spec 056 §4) from
// internal/api's renderWeb, which computes it once per request, down to
// layout.templ's top bar, without widening PageProps or the Page(p
// PageProps) signature every page already calls. templ's generated Render
// methods thread the same context.Context to every nested component, so a
// value stashed here before the top-level Render call is readable from any
// templ file without being passed explicitly. stdlib-only by construction —
// internal/ui imports nothing beyond stdlib, internal/model and the templ
// runtime.

import "context"

// inboxDotKey is the context key for the inbox indicator's has-items flag.
type inboxDotKey struct{}

// WithInboxDot returns ctx carrying whether the signed-in actor has at least
// one item waiting in their inbox. Callers pass the ctx returned from this to
// templ.Component.Render; internal/api's renderWeb is the only caller, so
// this is computed exactly once per request (spec 056 §4).
func WithInboxDot(ctx context.Context, has bool) context.Context {
	return context.WithValue(ctx, inboxDotKey{}, has)
}

// inboxDot reports the flag WithInboxDot set on ctx, or false when it was
// never set — an unauthenticated request, a store error renderWeb already
// logged, or a test that renders a component directly. The indicator must
// never fail a page, so "unset" and "false" render identically: no dot.
func inboxDot(ctx context.Context) bool {
	has, _ := ctx.Value(inboxDotKey{}).(bool)
	return has
}
