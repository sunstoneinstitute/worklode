package mcp

// levenshtein returns the edit distance between a and b, counted in runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// similarity is 1 minus the edit distance over the longer length: 1 for
// equal strings, 0 for nothing in common. Two empty strings are equal.
func similarity(a, b string) float64 {
	n := max(len([]rune(a)), len([]rune(b)))
	if n == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(n)
}
