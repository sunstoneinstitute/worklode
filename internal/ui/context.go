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

import (
	"context"
	"strings"
	"unicode"
)

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

// adminKey is the context key for the signed-in actor's admin role.
type adminKey struct{}

// WithAdmin returns ctx carrying whether the signed-in actor holds the admin
// role. internal/api's renderWeb is the only caller, so the roles are read
// once per request from the same Subject the route guards used.
func WithAdmin(ctx context.Context, is bool) context.Context {
	return context.WithValue(ctx, adminKey{}, is)
}

// isAdmin reports the flag WithAdmin set on ctx, or false when it was never
// set — an unauthenticated request, or a test rendering a component directly.
// "unset" and "false" render identically, so an admin-only card is absent
// rather than half-rendered when the caller forgot to set it.
func isAdmin(ctx context.Context) bool {
	is, _ := ctx.Value(adminKey{}).(bool)
	return is
}

// actorKey is the context key for the signed-in actor's name, for the avatar.
type actorKey struct{}

// WithActor returns ctx carrying the signed-in actor's id and display name
// (either may be empty). internal/api's renderWeb is the only caller.
func WithActor(ctx context.Context, id, displayName string) context.Context {
	name := displayName
	if strings.TrimSpace(name) == "" {
		name = id
	}
	return context.WithValue(ctx, actorKey{}, name)
}

// actorInitials is the avatar text for the actor on ctx: see initials.
func actorInitials(ctx context.Context) string {
	name, _ := ctx.Value(actorKey{}).(string)
	return initials(name)
}

// initials returns the uppercased first letters of up to the first two words
// of name, or "?" when name has none.
func initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
