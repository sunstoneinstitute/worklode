package derive

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// Dublin Core hasPart; the other reused IRIs come from graphproj, wl: terms
// from iri.Term.
const dctHasPart = "http://purl.org/dc/terms/hasPart"

// LayoutTriples derives the observed/repo-layout document (spec 007
// deriver 2): the repo's dct:hasPart edge to each manifest component, each
// component typed wl:Component, and every unmatched path collapsed to its
// top-level prefix as a wl:unmatchedPath gap. Dot-directories (.git,
// .worklode, .github, …) are infrastructure, not coverage gaps.
//
// "The repo's layout" is what the repo tracks — see repoFiles.
func LayoutTriples(ctx context.Context, root, host, owner, name string, m *manifest.Manifest) ([]byte, error) {
	repo := iri.Repo(host, owner, name)
	var ts []graphproj.Triple
	for _, c := range m.Components {
		ts = append(ts,
			graphproj.Triple{S: repo, P: dctHasPart, O: graphproj.IRIRef(c.IRI)},
			graphproj.Triple{S: c.IRI, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Component"))},
		)
	}

	files, err := repoFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	gaps := map[string]bool{}
	for _, rel := range files {
		if hasDotSegment(rel) {
			continue
		}
		if _, ok := m.Match(rel); !ok {
			top, _, _ := strings.Cut(rel, "/")
			gaps[top] = true
		}
	}

	prefixes := make([]string, 0, len(gaps))
	for p := range gaps {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		ts = append(ts, graphproj.Triple{S: repo, P: iri.Term("unmatchedPath"), O: graphproj.Text(p)})
	}
	return graphproj.Document(ts), nil
}

// repoFiles lists the files to attribute to components, relative to root and
// slash-separated.
//
// Tracked files, not a filesystem walk. `.gitignore` names build output that
// no dot-prefix rule catches — worklode's own lists `bin/`, `data/`, `wl`,
// `*.db` — so a walk reports whatever happens to be lying around as unmatched
// paths, and the document's content hash then depends on whether anyone ran a
// build. That breaks spec 007 §2's deriver contract ("Deterministic. Same
// inputs -> same triples") both ways: Run's hash short-circuit never fires, so
// every run re-PUTs, and `lode graph gaps` reports `bin` to a user as a component
// coverage gap. Untracked files are not part of the repo's layout; the tracked
// set is, and it is the same set on every machine.
//
// A root that is not inside a git work tree — a test fixture, an unpacked
// tarball — has no tracked set to read, so it falls back to the walk.
func repoFiles(ctx context.Context, root string) ([]string, error) {
	if gitexec.CmdContext(ctx, root, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return walkFiles(root)
	}
	// No --full-name: paths come back relative to root, which is what a root
	// below the repo toplevel needs. -z because git otherwise C-quotes any
	// path holding a quote, a backslash or a newline.
	out, err := gitexec.CmdContext(ctx, root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files in %s: %w", root, err)
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// walkFiles enumerates every file under root, skipping dot-directories
// outright so a stray .git object store is never read.
func walkFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return files, nil
}

// hasDotSegment reports whether any path segment is dot-prefixed — the
// infrastructure filter, applied to tracked paths (.github/workflows/ci.yml,
// .gitignore) and walked ones alike.
func hasDotSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
