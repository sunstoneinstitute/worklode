package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestDiffTokens(t *testing.T) {
	got := diffTokens("a  b\n\nc")
	want := []string{"a", "  ", "b", "\n\n", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffTokens = %q, want %q", got, want)
	}
	for _, in := range []string{"", " a b ", "\n\nx y\t", "  "} {
		if j := strings.Join(diffTokens(in), ""); j != in {
			t.Errorf("join(diffTokens(%q)) = %q", in, j)
		}
	}
}

func TestWordDiff(t *testing.T) {
	kinds := func(ops []DiffOp) []string {
		var k []string
		for _, o := range ops {
			k = append(k, o.Kind)
		}
		return k
	}
	tests := []struct {
		name, from, to string
		want           []string
	}{
		{"changed word", "the quick fox", "the slow fox", []string{"equal", "delete", "insert", "equal"}},
		{"appended word", "the fox", "the fox jumps", []string{"equal", "insert"}},
		{"identical", "the fox", "the fox", []string{"equal"}},
		{"empty", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kinds(WordDiff(tt.from, tt.to))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("kinds = %v, want %v", got, tt.want)
			}
		})
	}
	ops := WordDiff("the quick fox", "the slow fox")
	if ops[1].Text != "quick" || ops[2].Text != "slow" {
		t.Fatalf("ops = %+v", ops)
	}
}

func diffFor(from, to string) Diff {
	return Diff{Ref: "ZZ-REQ-7", From: 2, To: 3, Ops: WordDiff(from, to)}
}

func TestDiffWritePlain(t *testing.T) {
	var b bytes.Buffer
	diffWrite(&b, diffFor("the quick fox\n\nends", "the slow fox\n\nends"), false)
	want := "ZZ-REQ-7  v2 → v3\n\nthe [-quick-]{+slow+} fox\n\nends\n"
	if b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}

	b.Reset()
	diffWrite(&b, diffFor("same text", "same text"), false)
	if s := b.String(); s != "ZZ-REQ-7  v2 → v3\n\nsame text\n" || strings.Contains(s, "[-") || strings.Contains(s, "{+") {
		t.Fatalf("identical: %q", s)
	}
}

func TestDiffRenderBuffer(t *testing.T) {
	var b bytes.Buffer
	DiffRender(&b, diffFor("the quick fox\n\nends", "the slow fox\n\nends"))
	want := "ZZ-REQ-7  v2 → v3\n\nthe [-quick-]{+slow+} fox\n\nends\n"
	if b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
}

func TestDiffWriteColor(t *testing.T) {
	var b bytes.Buffer
	diffWrite(&b, diffFor("the quick fox", "the slow fox"), true)
	s := b.String()
	for _, w := range []string{sgrRed + "quick" + sgrReset, sgrGreen + "slow" + sgrReset} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in %q", w, s)
		}
	}
	if strings.Contains(s, "[-") {
		t.Errorf("plain markers in colour output: %q", s)
	}
}
