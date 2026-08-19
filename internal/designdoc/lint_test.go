package designdoc

import (
	"strings"
	"testing"
)

func TestLintAnchors(t *testing.T) {
	cases := map[string]struct {
		src  string
		want []string // substrings, one per expected finding, in order
	}{
		"clean": {
			src: "# T\n\n## 1. A {#sec-1}\n\nx\n\n## 2. B {#sec-2}\n\ny\n",
		},
		"unanchored headings are not a defect": {
			src: "# T\n\n## A\n\nx\n\n### B\n\ny\n",
		},
		"duplicate anchor": {
			src:  "# T\n\n## A {#sec-1}\n\nx\n\n## B {#sec-1}\n\ny\n",
			want: []string{`anchor #sec-1 is claimed by both "A" and "B"`},
		},
		"anchor disagrees with number": {
			src:  "# T\n\n## 1. A {#sec-9}\n\nx\n",
			want: []string{`heading "A" is numbered 1 but anchored #sec-9`},
		},
		"every defect is reported, not just the first": {
			src: "# T\n\n## 1. A {#sec-9}\n\nx\n\n## B {#sec-2}\n\ny\n\n## C {#sec-2}\n\nz\n",
			want: []string{
				`heading "A" is numbered 1 but anchored #sec-9`,
				`anchor #sec-2 is claimed by both "B" and "C"`,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := LintAnchors(doc)
			if len(got) != len(tc.want) {
				t.Fatalf("LintAnchors = %v, want %d findings", got, len(tc.want))
			}
			for i, want := range tc.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("finding %d = %q, want it to contain %q", i, got[i], want)
				}
			}
		})
	}
}
