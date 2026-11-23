package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/pmezard/go-difflib/difflib"
)

// DiffOp is one run of a word diff. Kind is "equal", "delete" or "insert".
type DiffOp struct{ Kind, Text string }

// Diff is the change between two versions of one subject (WL-REQ-1908).
type Diff struct {
	Ref      string
	From, To int
	Ops      []DiffOp
}

// diffTokens splits s into maximal runs of whitespace and of non-whitespace.
// Joining the tokens gives s back.
func diffTokens(s string) []string {
	var toks []string
	start := 0
	rs := []rune(s)
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || unicode.IsSpace(rs[i]) != unicode.IsSpace(rs[i-1]) {
			toks = append(toks, string(rs[start:i]))
			start = i
		}
	}
	return toks
}

// WordDiff diffs from against to token by token. autoJunk is off because the
// default heuristic stops matching the single-space token on long inputs.
func WordDiff(from, to string) []DiffOp {
	a, b := diffTokens(from), diffTokens(to)
	var ops []DiffOp
	for _, c := range difflib.NewMatcherWithJunk(a, b, false, nil).GetOpCodes() {
		switch c.Tag {
		case 'e':
			ops = append(ops, DiffOp{"equal", strings.Join(a[c.I1:c.I2], "")})
		case 'd':
			ops = append(ops, DiffOp{"delete", strings.Join(a[c.I1:c.I2], "")})
		case 'i':
			ops = append(ops, DiffOp{"insert", strings.Join(b[c.J1:c.J2], "")})
		case 'r':
			ops = append(ops,
				DiffOp{"delete", strings.Join(a[c.I1:c.I2], "")},
				DiffOp{"insert", strings.Join(b[c.J1:c.J2], "")})
		}
	}
	return ops
}

// DiffRender writes d to w, coloured when w is an interactive terminal.
func DiffRender(w io.Writer, d Diff) {
	_, tty := terminalFd(w)
	diffWrite(w, d, tty && colorEnabled())
}

func diffWrite(w io.Writer, d Diff, color bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  v%d → v%d\n\n", d.Ref, d.From, d.To)
	for _, op := range d.Ops {
		switch {
		case op.Kind == "delete" && color:
			b.WriteString(sgrRed + op.Text + sgrReset)
		case op.Kind == "insert" && color:
			b.WriteString(sgrGreen + op.Text + sgrReset)
		case op.Kind == "delete":
			b.WriteString("[-" + op.Text + "-]")
		case op.Kind == "insert":
			b.WriteString("{+" + op.Text + "+}")
		default:
			b.WriteString(op.Text)
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	io.WriteString(w, out+"\n")
}
