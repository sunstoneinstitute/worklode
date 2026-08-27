// Package overview implements spec 007's read side: standing queries over
// the two-layer graph, the frontier mirror, and estimate-free critical path
// (D12). Everything is computed on read — nothing is cached or stored.
package overview

import "sort"

// Analysis is the result of one critical-path pass over the combined
// dependency DAG (blocks ∪ requires, unit weights).
type Analysis struct {
	// Depth is the longest predecessor-chain length ending at each node.
	Depth map[string]int
	// FanOut counts the distinct nodes transitively downstream of each node.
	// Nil unless AnalyzeWithFanOut was used: it is the one measure here that
	// costs more than a topological pass, and only the critical path reports
	// it.
	FanOut map[string]int
	// Critical marks nodes lying on some longest chain.
	Critical map[string]bool
	// Cycles lists strongly connected components with a cycle (size > 1, or
	// a self-loop) — data errors excluded from the numbers above and
	// surfaced as their own finding (spec 007 §Cycle handling).
	Cycles [][]string
}

// Analyze runs the longest-path pass of spec 007 §Critical path v1 over edges
// (from must precede to) plus any isolated extra nodes (tasks with no edges
// still appear with depth 0). Depth, Critical and Cycles cost one topological
// pass; FanOut is left nil.
func Analyze(edges [][2]string, extraNodes []string) Analysis {
	return analyze(edges, extraNodes, false)
}

// AnalyzeWithFanOut is Analyze plus the transitive fan-out closure.
//
// Separate because the closure is the expensive half: it materialises the set
// of nodes reachable from each node, so it costs O(V·reach) time and memory
// against the O(V+E) of everything else. Measured on one machine at the time
// this split was made, a 1000-node chain took 105ms and allocated 45MB per
// call, against ~0 for the rest; BenchmarkAnalyze in this package is that
// measurement, kept so the next person deciding whether to cache or bitset it
// has numbers rather than a hunch.
//
// Only CriticalPath reports fan-out. /frontier annotates its rows from
// store.Frontier's SQL fan-out instead, so the three surfaces that used to pay
// for this closure and throw it away no longer do.
func AnalyzeWithFanOut(edges [][2]string, extraNodes []string) Analysis {
	return analyze(edges, extraNodes, true)
}

func analyze(edges [][2]string, extraNodes []string, wantFanOut bool) Analysis {
	nodes := map[string]bool{}
	for _, e := range edges {
		nodes[e[0]], nodes[e[1]] = true, true
	}
	for _, n := range extraNodes {
		nodes[n] = true
	}

	cycles := cyclicSCCs(edges)
	inCycle := map[string]bool{}
	for _, scc := range cycles {
		for _, n := range scc {
			inCycle[n] = true
		}
	}

	succ := map[string][]string{}
	pred := map[string][]string{}
	indeg := map[string]int{}
	for n := range nodes {
		if !inCycle[n] {
			indeg[n] = 0
		}
	}
	for _, e := range edges {
		if inCycle[e[0]] || inCycle[e[1]] {
			continue
		}
		succ[e[0]] = append(succ[e[0]], e[1])
		pred[e[1]] = append(pred[e[1]], e[0])
		indeg[e[1]]++
	}

	// Kahn topological order over the acyclic remainder.
	var order, queue []string
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue) // determinism
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range succ[n] {
			if indeg[m]--; indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}

	depth := map[string]int{}
	for _, n := range order {
		d := 0
		for _, p := range pred[n] {
			if depth[p]+1 > d {
				d = depth[p] + 1
			}
		}
		depth[n] = d
	}

	// down[n] = longest chain length from n forward; critical iff
	// depth[n] + down[n] == max chain length.
	down := map[string]int{}
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		d := 0
		for _, m := range succ[n] {
			if down[m]+1 > d {
				d = down[m] + 1
			}
		}
		down[n] = d
	}
	maxChain := 0
	for _, n := range order {
		if depth[n]+down[n] > maxChain {
			maxChain = depth[n] + down[n]
		}
	}
	critical := map[string]bool{}
	for _, n := range order {
		critical[n] = maxChain > 0 && depth[n]+down[n] == maxChain
	}

	var fanOut map[string]int
	if wantFanOut {
		// Transitive fan-out by reverse-topological set union.
		reach := map[string]map[string]bool{}
		fanOut = map[string]int{}
		for i := len(order) - 1; i >= 0; i-- {
			n := order[i]
			r := map[string]bool{}
			for _, m := range succ[n] {
				r[m] = true
				for x := range reach[m] {
					r[x] = true
				}
			}
			reach[n] = r
			fanOut[n] = len(r)
		}
	}

	return Analysis{Depth: depth, FanOut: fanOut, Critical: critical, Cycles: cycles}
}

// cyclicSCCs returns Tarjan strongly connected components that contain a
// cycle: size > 1, or a single node with a self-loop.
func cyclicSCCs(edges [][2]string) [][]string {
	succ := map[string][]string{}
	selfLoop := map[string]bool{}
	nodes := map[string]bool{}
	for _, e := range edges {
		succ[e[0]] = append(succ[e[0]], e[1])
		nodes[e[0]], nodes[e[1]] = true, true
		if e[0] == e[1] {
			selfLoop[e[0]] = true
		}
	}
	var (
		index, lowlink = map[string]int{}, map[string]int{}
		onStack        = map[string]bool{}
		stack          []string
		counter        int
		out            [][]string
		strongconnect  func(v string)
	)
	strongconnect = func(v string) {
		index[v], lowlink[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ[v] {
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && index[w] < lowlink[v] {
				lowlink[v] = index[w]
			}
		}
		if lowlink[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 || selfLoop[scc[0]] {
				out = append(out, scc)
			}
		}
	}
	ordered := make([]string, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	for _, n := range ordered {
		if _, seen := index[n]; !seen {
			strongconnect(n)
		}
	}
	return out
}

// OpenSubgraph filters a task DAG to the edges whose both ends are open:
// spec 007 §4's rule that criticality and fan-out are computed over
// remaining work. A closed task no longer blocks its dependents and has
// nothing left to unblock, so edges touching one contribute history (depth,
// which Analyze still computes over the full DAG) and never criticality.
func OpenSubgraph(edges [][2]string, closed map[string]bool) [][2]string {
	var out [][2]string
	for _, e := range edges {
		if !closed[e[0]] && !closed[e[1]] {
			out = append(out, e)
		}
	}
	return out
}
