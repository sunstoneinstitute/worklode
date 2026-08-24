package overview

import (
	"fmt"
	"testing"
)

// The numbers behind AnalyzeWithFanOut's warning, so whoever next weighs a
// bitset closure or an LRU against leaving it alone has a measurement rather
// than a hunch. Not a CI gate: `go test` does not run benchmarks, and a
// nanosecond threshold on a shared runner would be flaky.
//
// Measured once on an Intel Xeon Gold 5412U, Go 1.26, per call, with and
// without the closure:
//
//	chain    100    0.38ms   0.1MB  →  1.7ms    0.5MB
//	chain   1000    5.2ms    1.7MB  →  103ms     46MB
//	sparse  1000    4.9ms    1.5MB  →  8.0ms    2.5MB
//	sparse 10000     56ms     13MB  →  100ms     26MB
//
// A 1000-node chain is the shape that hurts: the closure costs 20x the time
// and 26x the memory of everything else in the pass. "Sparse" is no defence,
// only a smaller multiplier, because a node still reaches its whole subtree.
//
// If the closure ever needs to be cheap rather than merely rare, the options
// are a bitset closure (V²/8 bytes total, word-OR unions) or an LRU keyed on
// the edge-set hash. Neither is warranted while one surface computes it.

// chainEdges is the worst case: every node reaches every node below it, so
// the closure holds V²/2 entries.
func chainEdges(n int) [][2]string {
	e := make([][2]string, 0, n)
	for i := range n - 1 {
		e = append(e, [2]string{fmt.Sprintf("T-%d", i), fmt.Sprintf("T-%d", i+1)})
	}
	return e
}

// sparseEdges is a binary tree: few edges per node, but each node still
// reaches its whole subtree, which is what makes "sparse" no defence here.
func sparseEdges(n int) [][2]string {
	e := make([][2]string, 0, n)
	for i := range n {
		if 2*i+1 < n {
			e = append(e, [2]string{fmt.Sprintf("T-%d", i), fmt.Sprintf("T-%d", 2*i+1)})
		}
		if 2*i+2 < n {
			e = append(e, [2]string{fmt.Sprintf("T-%d", i), fmt.Sprintf("T-%d", 2*i+2)})
		}
	}
	return e
}

func BenchmarkAnalyze(b *testing.B) {
	for _, shape := range []struct {
		name  string
		build func(int) [][2]string
		sizes []int
	}{
		{"chain", chainEdges, []int{100, 1000}},
		{"sparse", sparseEdges, []int{1000, 10000}},
	} {
		for _, n := range shape.sizes {
			e := shape.build(n)
			b.Run(fmt.Sprintf("%s/%d/no-fanout", shape.name, n), func(b *testing.B) {
				for b.Loop() {
					Analyze(e, nil)
				}
			})
			b.Run(fmt.Sprintf("%s/%d/fanout", shape.name, n), func(b *testing.B) {
				for b.Loop() {
					AnalyzeWithFanOut(e, nil)
				}
			})
		}
	}
}
