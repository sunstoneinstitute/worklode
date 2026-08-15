// Package model holds every shape that crosses Worklode's HTTP boundary:
// entities, response projections, and request bodies. One declaration per
// shape — internal/store scans into these types, internal/api serializes
// them, internal/cli decodes them, internal/ui embeds them (ADR 036).
//
// This package imports the standard library only, so every layer can depend
// on it and nothing depends back. deps_test.go enforces that.
package model
