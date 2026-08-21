package derive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const dctRequires = "http://purl.org/dc/terms/requires"

// listedPackage is the subset of `go list -json` output the deriver reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
	Module     *struct {
		Path string
		Dir  string
	}
}

// componentOf maps a package to its owning component IRI via the manifest.
// The match key is the repo-relative path of the package's first Go file,
// because manifest globs (cmd/ingest/**) match files, not directories.
// "" means: not in this module, or matched by no component.
func componentOf(p listedPackage, moduleRoot string, m *manifest.Manifest) string {
	if p.Module == nil || p.Module.Dir != moduleRoot || len(p.GoFiles) == 0 {
		return ""
	}
	rel, err := filepath.Rel(moduleRoot, filepath.Join(p.Dir, p.GoFiles[0]))
	if err != nil {
		return ""
	}
	c, ok := m.Match(filepath.ToSlash(rel))
	if !ok {
		return ""
	}
	return c.IRI
}

// ImportsTriples turns a `go list -deps -json ./...` stream into the
// observed/go-imports document (spec 007 deriver 1): one
// <a> dct:requires <b> per pair of distinct components with at least one
// package-level import between them. Same-component and unmapped edges are
// dropped; graphproj.Document sorts and dedupes, so output is deterministic.
func ImportsTriples(goList io.Reader, moduleRoot string, m *manifest.Manifest) ([]byte, error) {
	dec := json.NewDecoder(goList)
	var pkgs []listedPackage
	byImportPath := map[string]string{} // import path → component IRI
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse go list output: %w", err)
		}
		pkgs = append(pkgs, p)
		byImportPath[p.ImportPath] = componentOf(p, moduleRoot, m)
	}

	var ts []graphproj.Triple
	for _, p := range pkgs {
		from := byImportPath[p.ImportPath]
		if from == "" {
			continue
		}
		for _, imp := range p.Imports {
			if to := byImportPath[imp]; to != "" && to != from {
				ts = append(ts, graphproj.Triple{S: from, P: dctRequires, O: graphproj.IRIRef(to)})
			}
		}
	}
	return graphproj.Document(ts), nil
}

// GoListDeps runs `go list -deps -json ./...` in repoRoot and returns its
// stdout, for DeriveImports; split out so tests feed a fixture stream.
func GoListDeps(ctx context.Context, repoRoot string) (io.Reader, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json", "./...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("go list -deps -json in %s: %w%s", repoRoot, err, msg)
	}
	return strings.NewReader(string(out)), nil
}
