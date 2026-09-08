// progress.go is the client and table for a project's derived progress
// (WL-SPEC-66 §7): what each spec says, and how much of it exists. The table
// prints the same model.ProjectProgress the cockpit's Progress page draws, so
// an agent reads what a person sees. It shows no completion percentage —
// 032 §4 forbids one.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/progress"
)

// ProjectProgress calls GET /api/v1/projects/{id}/progress.
func (c *Client) ProjectProgress(ctx context.Context, id string) (model.ProjectProgress, []byte, error) {
	return doJSON[model.ProjectProgress](ctx, c,
		http.MethodGet, "/api/v1/projects/"+url.PathEscape(id)+"/progress", nil, "progress")
}

// progressGroupLabels and progressGroupHelp are §1.3's group names and the
// one line saying why the group ranks where it does — the same words the page
// prints above each group.
var progressGroupLabels = map[string]string{
	"active": "Active", "planning": "Needs planning",
	"no_record": "No execution record", "built": "Built",
}

var progressGroupHelp = map[string]string{
	"active":    "work is claimable now",
	"planning":  "a human has to plan or accept",
	"no_record": "the record is missing, not the work",
	"built":     "nothing to do",
}

// ProgressTable prints one spec per line under its §1.3 group header: the
// spec's reference and title, the count of its sections in each state, and
// the act that moves it next. An empty group is omitted rather than printed
// as a heading with nothing under it.
func ProgressTable(w io.Writer, p model.ProjectProgress) {
	printed := false
	for _, g := range p.Groups {
		if len(g.Specs) == 0 {
			continue
		}
		if printed {
			fmt.Fprintln(w)
		}
		printed = true
		fmt.Fprintf(w, "%s · %d  (%s)\n", progressGroupLabels[g.Key], len(g.Specs), progressGroupHelp[g.Key])
		tbl := newTable(
			column{header: "REF"},
			titleColumn("TITLE"),
			column{header: "STATES"},
			titleColumn("NEXT"),
		)
		for _, s := range g.Specs {
			tbl.add(s.Ref, s.Title, progressStates(s.Sections), s.Next.Text)
		}
		tbl.flush(w)
	}
	if !printed {
		fmt.Fprintf(w, "no specs in %s\n", p.Project)
	}
}

// progressStates renders a spec's section counts by state, in the §2.1 bar
// order, omitting a state the spec has none of. Sections in "bound" are not
// owed (§1.4) and so are not counted — the same rule the page's strip draws
// by.
func progressStates(sections []model.ProgressSection) string {
	counts := make(map[string]int, len(progress.BarOrder))
	for _, sec := range sections {
		counts[sec.State]++
	}
	var parts []string
	for _, state := range progress.BarOrder {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ReplaceAll(state, "_", " "), n))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " · ")
}
