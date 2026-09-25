// taskprerequisites.go derives the task page's prerequisite graph (WL-877)
// from store.BlockerTree: the same rows GET /api/v1/tasks/{id}/blockers
// serves, so the page and the API cannot disagree about what holds a task.
package api

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// Card geometry in SVG pixels, and the most cards one drawing holds. Past the
// cap the nearest prerequisites are drawn and the rest are counted as hidden.
const (
	prereqCardW    = 200
	prereqCardH    = 58
	prereqColGap   = 56
	prereqRowGap   = 14
	prereqPad      = 8
	prereqLabelMax = 26
	prereqMaxCards = 60
)

// prerequisitesView lays out a blocker tree as a left-to-right layered graph.
// Nodes are deduplicated by ID and edges by (ID, Via), so a shared
// prerequisite is one card and a direct edge survives beside an indirect
// path. A task's column is its longest dependsOn distance from the target,
// computed over the graph with its cycle-closing edges set aside. title is
// the target's own, for its card. Nil when nothing holds the task.
func prerequisitesView(tree model.BlockerTree, title string) *ui.Prerequisites {
	if len(tree.Blockers) == 0 && len(tree.BlockingPlans) == 0 {
		return nil
	}
	root := tree.Root
	cards := map[string]*ui.PrerequisiteCard{}
	var order []string
	dependsOn := map[string][]string{}
	seen := map[[2]string]bool{}
	for _, b := range tree.Blockers {
		if b.ID != root && cards[b.ID] == nil {
			cards[b.ID] = &ui.PrerequisiteCard{ID: b.ID, Title: b.Title, Label: prereqLabel(b.Title), State: b.State}
			order = append(order, b.ID)
		}
		if b.Cycle && b.ID != root {
			cards[b.ID].Cycle = true
		}
		edge := [2]string{b.Via, b.ID}
		if seen[edge] {
			continue
		}
		seen[edge] = true
		dependsOn[b.Via] = append(dependsOn[b.Via], b.ID)
		if b.ID != root {
			cards[b.ID].NeededBy = append(cards[b.ID].NeededBy, b.Via)
		}
	}

	// Depth-first from the target: an edge to a task still on the stack
	// closes a cycle. Reverse postorder over the rest is a topological order,
	// in which longest distances relax in one pass.
	onStack, done := map[string]bool{}, map[string]bool{}
	back := map[[2]string]bool{}
	var post []string
	var visit func(string)
	visit = func(u string) {
		onStack[u] = true
		for _, v := range dependsOn[u] {
			switch {
			case onStack[v]:
				back[[2]string{u, v}] = true
				if c := cards[v]; c != nil {
					c.Cycle = true
				}
				if c := cards[u]; c != nil {
					c.Cycle = true
				}
			case !done[v]:
				visit(v)
			}
		}
		onStack[u], done[u] = false, true
		post = append(post, u)
	}
	visit(root)
	rank := map[string]int{root: 0}
	for i := len(post) - 1; i >= 0; i-- {
		u := post[i]
		for _, v := range dependsOn[u] {
			if !back[[2]string{u, v}] && rank[u]+1 > rank[v] {
				rank[v] = rank[u] + 1
			}
		}
	}

	v := &ui.Prerequisites{Remaining: len(order)}
	for _, id := range dependsOn[root] {
		if id != root {
			v.Direct++
		}
	}
	for _, id := range order {
		if len(dependsOn[id]) == 0 {
			v.Candidates = append(v.Candidates, id)
		}
		if cards[id].Cycle {
			v.Cycle = append(v.Cycle, id)
		}
	}
	seenPlan := map[int64]bool{}
	for _, p := range tree.BlockingPlans {
		if !seenPlan[p.ID] {
			seenPlan[p.ID] = true
			v.Plans = append(v.Plans, ui.PrerequisitePlan{Slug: p.Slug, Title: p.Title, Status: p.Status, URL: docPageURL(p.ID)})
		}
	}
	if len(order) == 0 {
		return v
	}

	// Nearest first: by column, then in the store's own row order.
	sorted := slices.Clone(order)
	slices.SortStableFunc(sorted, func(a, b string) int { return rank[a] - rank[b] })
	for _, id := range sorted {
		v.List = append(v.List, *cards[id])
	}
	drawn := sorted
	if len(drawn) > prereqMaxCards {
		drawn, v.Hidden = drawn[:prereqMaxCards], len(drawn)-prereqMaxCards
	}

	target := &ui.PrerequisiteCard{ID: root, Title: title, Label: prereqLabel(title), Target: true}
	cards[root] = target
	byRank := map[int][]*ui.PrerequisiteCard{0: {target}}
	maxRank, maxRows := 0, 1
	for _, id := range drawn {
		r := rank[id]
		byRank[r] = append(byRank[r], cards[id])
		maxRank = max(maxRank, r)
		maxRows = max(maxRows, len(byRank[r]))
	}
	pitch := prereqCardH + prereqRowGap
	for r := 0; r <= maxRank; r++ {
		col := byRank[r]
		offset := (maxRows - len(col)) * pitch / 2
		for i, c := range col {
			c.X = prereqPad + (maxRank-r)*(prereqCardW+prereqColGap)
			c.Y = prereqPad + offset + i*pitch
		}
	}
	v.Width = 2*prereqPad + (maxRank+1)*(prereqCardW+prereqColGap) - prereqColGap
	v.Height = 2*prereqPad + maxRows*pitch - prereqRowGap
	v.Cards = append(v.Cards, *target)
	for _, id := range drawn {
		v.Cards = append(v.Cards, *cards[id])
	}

	isDrawn := map[string]bool{root: true}
	for _, id := range drawn {
		isDrawn[id] = true
	}
	for _, u := range append([]string{root}, order...) {
		for _, p := range dependsOn[u] {
			if !isDrawn[u] || !isDrawn[p] {
				continue
			}
			v.Links = append(v.Links, ui.PrerequisiteLink{Path: prereqPath(cards[p], cards[u]), Cycle: back[[2]string{u, p}]})
		}
	}
	return v
}

// prereqPath is a cubic curve from the prerequisite's right edge to the
// dependent's left edge. The control points bow outward, so a cycle's
// backward edge still reads as a curve rather than a line through cards.
func prereqPath(from, to *ui.PrerequisiteCard) string {
	x1, y1 := from.X+prereqCardW, from.Y+prereqCardH/2
	x2, y2 := to.X, to.Y+prereqCardH/2
	bow := max((x2-x1)/2, prereqColGap)
	return fmt.Sprintf("M%d %d C%d %d %d %d %d %d", x1, y1, x1+bow, y1, x2-bow, y2, x2, y2)
}

// prereqLabel shortens a title to what fits on a card. The full title stays
// on the card's tooltip and in the list.
func prereqLabel(title string) string {
	r := []rune(strings.TrimSpace(title))
	if len(r) <= prereqLabelMax {
		return string(r)
	}
	return strings.TrimSpace(string(r[:prereqLabelMax-1])) + "…"
}
