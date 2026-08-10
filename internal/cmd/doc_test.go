package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func TestCheckSyncGate(t *testing.T) {
	for name, tc := range map[string]struct {
		g       syncGate
		force   bool
		wantErr string // "" = allowed
	}{
		"default clean":         {g: syncGate{Branch: "main", DefaultBranch: "main", Clean: true}},
		"off default":           {g: syncGate{Branch: "feat", DefaultBranch: "main", Clean: true}, wantErr: "not on the default branch"},
		"dirty":                 {g: syncGate{Branch: "main", DefaultBranch: "main", Clean: false}, wantErr: "working tree is dirty"},
		"no origin head":        {g: syncGate{Branch: "main", DefaultErr: errors.New("no default branch recorded"), Clean: true}, wantErr: "no default branch recorded"},
		"forced off default":    {g: syncGate{Branch: "feat", DefaultBranch: "main", Clean: false}, force: true},
		"forced no origin head": {g: syncGate{Branch: "feat", DefaultErr: errors.New("x"), Clean: false}, force: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkSyncGate(tc.g, tc.force)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("gate refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "--force") {
				t.Errorf("gate error %q does not mention --force", err)
			}
		})
	}
}

func TestCorpusToUpserts(t *testing.T) {
	in := []designdoc.CorpusDoc{{
		Filename: "034-x.md", Kind: "spec", Ordinal: "34",
		Status: "accepted", Title: "T", Source: []byte("body"),
		FrontmatterJSON: []byte(`{"status":"accepted"}`),
		Sections:        []designdoc.SectionMeta{{Anchor: "sec-1", Heading: "S", Depth: 2, Position: 0}},
		Edges:           []designdoc.EdgeMeta{{SrcAnchor: "sec-1", Rel: "amends", Target: "025-y.md", TargetAnchor: "sec-2"}},
	}}
	out := corpusToUpserts(in)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	u := out[0]
	if u.Kind != "spec" || u.Ordinal != "34" || u.Body != "body" ||
		string(u.Frontmatter) != `{"status":"accepted"}` ||
		len(u.Sections) != 1 || u.Sections[0].Anchor != "sec-1" ||
		len(u.Edges) != 1 || u.Edges[0].Rel != "amends" {
		t.Errorf("upsert = %+v", u)
	}
}
