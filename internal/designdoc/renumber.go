package designdoc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Renumber rewrites every addressable section's number and {#sec-N} anchor
// from its position in the heading tree, so the two cannot drift apart when a
// section is inserted, moved or removed. It is the port of scripts/secfmt.py,
// deleted with the rest of the corpus tooling when documents left the tree
// (055), and it keeps that script's rules:
//
//   - Numbers are normalised, never introduced. A heading the author left
//     unnumbered stays unnumbered, and any deeper sequence restarts under it,
//     so a document mixing an unnumbered preamble with numbered body sections
//     keeps its shape.
//   - The top level starts at 1, or at 0 when the author numbered the first
//     top-level section `0.` — the orientation section, numbered 0 so the body
//     still starts at 1. Only the top level may start at 0.
//   - A letter-suffixed section (`2.1a`, 025 §3) is a deliberate post-acceptance
//     insert. It keeps its number and consumes no counter slot.
//   - A heading deeper than depth is legal content and takes no number
//     (025 §6), so it is left exactly as it is.
//
// It mutates d in place; the caller renders the result with d.Bytes(). It
// changes no other text, so a section's body, its title and the document's
// frontmatter round-trip untouched.
//
// Two shapes have no derivable answer and are returned as an error naming
// every one of them at once, rather than guessed at: a numbered heading whose
// parent is unnumbered (there is no prefix to build on), and a letter-suffixed
// insert whose parent number has moved (renumbering it would defeat the
// suffix, whose whole point is to survive a renumbering).
func Renumber(d *Document, depth int) error {
	if depth < 1 {
		depth = 1
	}
	counters := make([]int, depth)
	counters[0] = firstTopNumber(d) - 1
	// seen says whether a level has been numbered yet, which counters cannot
	// express once 0 is a legal section number.
	seen := make([]bool, depth)

	var defects []string
	for _, s := range d.Sections {
		level := s.Level - 1 // H2 is level 1
		if level > depth {
			continue
		}
		if s.Number == "" {
			// Opens an unnumbered section; any deeper sequence restarts under it.
			resetBelow(counters, seen, level, depth)
			continue
		}
		if !numbered(seen[:level-1]) {
			defects = append(defects, fmt.Sprintf(
				"§%s %q is numbered but its parent is not, so the prefix cannot be derived",
				s.Number, s.Title))
			continue
		}
		prefix := joinCounters(counters[:level-1])

		number := s.Number
		if isInsert(number) {
			seen[level-1] = true
			parent := ""
			if i := strings.LastIndex(number, "."); i >= 0 {
				parent = number[:i]
			}
			if parent != prefix {
				defects = append(defects, fmt.Sprintf(
					"§%s %q: the insert's parent moved to §%s, and renumbering an insert would defeat the suffix",
					number, s.Title, prefix))
				continue
			}
		} else {
			counters[level-1]++
			seen[level-1] = true
			resetBelow(counters, seen, level, depth)
			number = joinCounters(counters[:level])
		}
		s.Number = number
		s.Anchor = "sec-" + number
	}
	if len(defects) > 0 {
		return errors.New(strings.Join(defects, "\n"))
	}
	return nil
}

// firstTopNumber reads the number the document's first numbered top-level
// section carries, which is what decides whether the sequence opens at 0 or 1.
func firstTopNumber(d *Document) int {
	for _, s := range d.Sections {
		if s.Level == 2 && s.Number != "" {
			if s.Number == "0" {
				return 0
			}
			return 1
		}
	}
	return 1
}

// isInsert reports the letter-suffixed form of 025 §3: "2.1a".
func isInsert(number string) bool {
	if number == "" {
		return false
	}
	last := number[len(number)-1]
	return last >= 'a' && last <= 'z'
}

func numbered(seen []bool) bool {
	for _, ok := range seen {
		if !ok {
			return false
		}
	}
	return true
}

func resetBelow(counters []int, seen []bool, level, depth int) {
	for j := level; j < depth; j++ {
		counters[j], seen[j] = 0, false
	}
}

func joinCounters(counters []int) string {
	parts := make([]string, len(counters))
	for i, c := range counters {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ".")
}
