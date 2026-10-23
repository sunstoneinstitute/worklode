package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// InlineRefsRender prints the "Referenced by" block of a node's detail: one
// line per field of another node that names it, with the lines it does so on
// (WL-REQ-1852). Nothing is printed when no node does.
func InlineRefsRender(w io.Writer, refs model.InlineRefs) {
	if len(refs.In) == 0 {
		return
	}
	fmt.Fprintln(w, "\nReferenced by:")
	for _, r := range refs.In {
		lines := make([]string, len(r.Lines))
		for i, n := range r.Lines {
			lines[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(w, "  %s  %s", r.FromRef, r.Field)
		if len(lines) > 0 {
			fmt.Fprintf(w, "  line %s", strings.Join(lines, ", "))
		}
		fmt.Fprintln(w)
	}
}
