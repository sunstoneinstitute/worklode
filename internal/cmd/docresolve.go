package cmd

import (
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The document-reference grammar lives in internal/designdoc (refresolve.go)
// since WL-301, shared with the cockpit's /docs/ref/ redirect; these aliases
// keep this package's call sites and tests reading as before.

func resolveDocRef(docs []model.Doc, projectKey, ref string) (model.Doc, string, error) {
	return designdoc.ResolveRef(docs, projectKey, ref)
}

func checkDocKind(doc model.Doc, typ string) error { return designdoc.CheckDocKind(doc, typ) }

func notFoundRefError(ref string) error { return designdoc.NotFoundRefError(ref) }

func looksLikePath(base string) bool { return designdoc.LooksLikePath(base) }
