// The document-reference redirect (WL-301): GET /docs/ref/{ref...} resolves
// any 026 §3 reference — a corpus path, a filename, a number form, a slug,
// or the <KEY>-<TYPE>-<n> shorthand — against the backbone's documents and
// redirects to the document's page. The #sec fragment never reaches the
// server; the browser re-applies it to the redirect target, which is what
// lets mdrender's autolinked references and the doc page's relation links
// carry their section along.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// docRefRedirect handles GET /docs/ref/{ref...}.
func (s *server) docRefRedirect(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusNotFound, "empty document reference")
		return
	}
	d, err := s.resolveDocRefWeb(r.Context(), ref, r.URL.Query().Get(mdrender.HomeParam))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	p, err := s.st.GetProject(r.Context(), d.Project)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	http.Redirect(w, r, docCanonicalURL(d, p.Key), http.StatusFound)
}

// resolveDocRefWeb resolves ref against every live document, using the same
// pure grammar `lode show` resolves with (designdoc.ResolveRef). The
// org-wide resolution has no current project, so the shorthand's key is
// answered the way `lode show`'s tier 2 answers it (026 §4.2): the project
// whose key it is supplies the candidates. A number form that is ambiguous
// across projects reports the candidates rather than picking one.
//
// home is the ?p=<KEY> the renderer puts on a number-form link (WL-723).
// Document numbers are per-project sequences, so a bare "029" names one
// document in the project the referring body belongs to and a different one
// everywhere else; resolving within that project first is what makes the link
// land. An unknown key, or a ref that names nothing there, falls through to
// the org-wide resolution below.
func (s *server) resolveDocRefWeb(ctx context.Context, ref, home string) (model.Doc, error) {
	docs, err := s.st.ListDocs(ctx, store.DocFilter{})
	if err != nil {
		return model.Doc{}, err
	}
	if home != "" {
		if d, ok := s.resolveDocRefIn(ctx, docs, home, ref); ok {
			return d, nil
		}
	}
	d, _, err := designdoc.ResolveRef(docs, "", ref)
	if err == nil {
		return d, nil
	}
	var unresolved *designdoc.UnresolvedError
	if !errors.As(err, &unresolved) {
		return model.Doc{}, err
	}
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		return model.Doc{}, err
	}
	for _, p := range projects {
		if p.Key != unresolved.Key {
			continue
		}
		d, _, err := designdoc.ResolveRef(scopeDocsToProject(docs, p.ID), p.Key, ref)
		return d, err
	}
	return model.Doc{}, unresolved
}

// resolveDocRefIn resolves ref against one project's documents alone. It
// reports false — never an error — when the project key is unknown or the ref
// names nothing there, because both are cases for the wider search the caller
// falls through to.
func (s *server) resolveDocRefIn(ctx context.Context, docs []model.Doc, key, ref string) (model.Doc, bool) {
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		return model.Doc{}, false
	}
	for _, p := range projects {
		if p.Key != key {
			continue
		}
		d, _, err := designdoc.ResolveRef(scopeDocsToProject(docs, p.ID), p.Key, ref)
		return d, err == nil
	}
	return model.Doc{}, false
}

// scopeDocsToProject is the candidate set of one project's live documents.
func scopeDocsToProject(docs []model.Doc, project string) []model.Doc {
	scoped := docs[:0:0]
	for _, d := range docs {
		if d.Project == project {
			scoped = append(scoped, d)
		}
	}
	return scoped
}

// refShortcut handles GET /{ref}: a bare reference at the root, so a task id
// or a document reference pasted into the address bar lands on its page.
// Every literal route (/docs, /work, /reviews, …) is more specific than this
// wildcard and still wins, so the shortcut only sees paths nothing else
// claims.
//
// Task ids resolve first because the lookup is one indexed row, and because
// <KEY>-<n> and <KEY>-<TYPE>-<n> are not distinguishable by shape alone —
// model.SplitTaskID reads "WL-SPEC-59" as key "WL-SPEC". Whatever the task
// lookup misses falls through to the same resolver /docs/ref/{ref...} uses,
// which already answers every 026 §3 reference form.
func (s *server) refShortcut(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusNotFound, "empty reference")
		return
	}
	if _, err := s.st.GetTask(r.Context(), ref); err == nil {
		http.Redirect(w, r, "/tasks/"+url.PathEscape(ref), http.StatusFound)
		return
	}
	s.docRefRedirect(w, r)
}
