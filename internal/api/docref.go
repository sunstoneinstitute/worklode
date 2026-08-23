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
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
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
	d, err := s.resolveDocRefWeb(r.Context(), ref)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	p, err := s.st.GetProject(r.Context(), d.Project)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	url := docPageURL(d.ID)
	if d.Number != 0 {
		url = "/docs/" + docWebRef(d, p.Key)
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// resolveDocRefWeb resolves ref against every live document, using the same
// pure grammar `lode show` resolves with (designdoc.ResolveRef). The
// org-wide resolution has no current project, so the shorthand's key is
// answered the way `lode show`'s tier 2 answers it (026 §4.2): the project
// whose key it is supplies the candidates. A number form that is ambiguous
// across projects reports the candidates rather than picking one.
func (s *server) resolveDocRefWeb(ctx context.Context, ref string) (model.Doc, error) {
	docs, err := s.st.ListDocs(ctx, store.DocFilter{})
	if err != nil {
		return model.Doc{}, err
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
		scoped := docs[:0:0]
		for _, doc := range docs {
			if doc.Project == p.ID {
				scoped = append(scoped, doc)
			}
		}
		d, _, err := designdoc.ResolveRef(scoped, p.Key, ref)
		return d, err
	}
	return model.Doc{}, unresolved
}
