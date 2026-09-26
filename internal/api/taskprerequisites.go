// taskprerequisites.go derives the task page's prerequisite tree (WL-877,
// WL-902) from store.BlockerTree: the same rows GET
// /api/v1/tasks/{id}/blockers serves, so the page and the API cannot
// disagree about what holds a task.
package api

import (
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// prereqMaxCards is the most tasks the tree draws in full. Past it the
// nearest are drawn and the rest are counted as hidden; the list keeps all.
const prereqMaxCards = 60

// prerequisitesView lays out a blocker tree top-down, one level per
// dependsOn hop. Tasks are deduplicated by ID and edges by (ID, Via). A
// breadth-first walk from the target gives each task its shallowest
// position, where it is drawn in full; every other edge into it becomes a
// reference, marked Cycle when the task it points at depends back on the one
// holding the reference. Nil when nothing holds the task.
func prerequisitesView(tree model.BlockerTree) *ui.Prerequisites {
	if len(tree.Blockers) == 0 && len(tree.BlockingPlans) == 0 {
		return nil
	}
	root := tree.Root
	info := map[string]*ui.PrerequisiteNode{}
	dependsOn := map[string][]string{}
	seen := map[[2]string]bool{}
	for _, b := range tree.Blockers {
		if b.ID != root && info[b.ID] == nil {
			info[b.ID] = &ui.PrerequisiteNode{ID: b.ID, Title: b.Title, State: b.State}
		}
		edge := [2]string{b.Via, b.ID}
		if seen[edge] {
			continue
		}
		seen[edge] = true
		dependsOn[b.Via] = append(dependsOn[b.Via], b.ID)
		if b.ID != root {
			info[b.ID].NeededBy = append(info[b.ID].NeededBy, b.Via)
		}
	}

	// Breadth-first from the target: the first edge to reach a task is its
	// tree edge, and the walk order is nearest first.
	parent := map[string]string{}
	order := []string{}
	for queue := []string{root}; len(queue) > 0; queue = queue[1:] {
		u := queue[0]
		for _, v := range dependsOn[u] {
			if _, placed := parent[v]; placed || v == root {
				continue
			}
			parent[v] = u
			order = append(order, v)
			queue = append(queue, v)
		}
	}

	v := &ui.Prerequisites{Remaining: len(order)}
	v.Levels, v.Below = below(root, root, dependsOn)
	for _, id := range dependsOn[root] {
		if id != root {
			v.Direct++
		}
	}
	for _, id := range order {
		n := info[id]
		n.Levels, n.Below = below(id, root, dependsOn)
		if len(dependsOn[id]) == 0 {
			v.Candidates = append(v.Candidates, id)
		} else if n.State == "ready" {
			// Blocked is derived, never stored: a ready task with an open
			// blocker cannot be claimed.
			n.State = "blocked"
		}
		if reaches(id, id, dependsOn) {
			n.Cycle = true
			v.Cycle = append(v.Cycle, id)
		}
		v.List = append(v.List, *n)
	}
	seenPlan := map[int64]bool{}
	for _, p := range tree.BlockingPlans {
		if !seenPlan[p.ID] {
			seenPlan[p.ID] = true
			v.Plans = append(v.Plans, ui.PrerequisitePlan{Slug: p.Slug, Title: p.Title, Status: p.Status, URL: docPageURL(p.ID)})
		}
	}

	drawn := map[string]bool{}
	for i, id := range order {
		if i < prereqMaxCards {
			drawn[id] = true
		}
	}
	v.Hidden = len(order) - len(drawn)

	var build func(u string) []ui.PrerequisiteNode
	build = func(u string) []ui.PrerequisiteNode {
		var out []ui.PrerequisiteNode
		for _, c := range dependsOn[u] {
			switch {
			case c == root:
				out = append(out, ui.PrerequisiteNode{ID: root, Ref: true, Cycle: true})
			case !drawn[c]:
				// Past the cap: counted in Hidden, carried by the list.
			case parent[c] == u:
				n := *info[c]
				n.Cycle = false
				n.Children = build(c)
				out = append(out, n)
			default:
				n := info[c]
				out = append(out, ui.PrerequisiteNode{ID: c, Title: n.Title, State: n.State, Ref: true, Cycle: reaches(c, u, dependsOn)})
			}
		}
		return out
	}
	v.Tree = build(root)
	return v
}

// below returns how many levels sit beneath id and how many distinct tasks
// they hold: the farthest shortest-path distance and the reachable set, so a
// cycle neither loops nor counts a task twice. The target is never counted.
func below(id, root string, dependsOn map[string][]string) (levels, tasks int) {
	dist := map[string]int{id: 0}
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		u := queue[0]
		for _, w := range dependsOn[u] {
			if _, ok := dist[w]; !ok {
				dist[w] = dist[u] + 1
				levels = max(levels, dist[w])
				queue = append(queue, w)
			}
		}
	}
	tasks = len(dist) - 1
	if _, ok := dist[root]; ok && id != root {
		tasks--
	}
	return levels, tasks
}

// reaches reports whether to is reachable from from by one or more
// dependsOn hops.
func reaches(from, to string, dependsOn map[string][]string) bool {
	seen := map[string]bool{}
	stack := slices.Clone(dependsOn[from])
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if u == to {
			return true
		}
		if !seen[u] {
			seen[u] = true
			stack = append(stack, dependsOn[u]...)
		}
	}
	return false
}
