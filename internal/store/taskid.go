package store

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// splitTaskID splits a <KEY>-<n> task id into its key and numeric parts. ok
// is false for a malformed id: one with no '-' or a non-numeric suffix (e.g.
// "WL", "bogus", "WL-x"). Keys never contain '-' (they match
// ^[A-Z][A-Z0-9]{1,9}$), so the split is on the final '-'.
func splitTaskID(id string) (key string, n int, ok bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// CompareTaskIDs orders <KEY>-<n> task ids by key lexically, then by n
// numerically, so WL-9 sorts before WL-10 (a plain string compare gets this
// wrong). It returns -1, 0, or +1 like cmp.Compare and plugs directly into
// slices.SortFunc. Malformed ids (see splitTaskID) sort after all well-formed
// ids and lexically among themselves, keeping the order total and panic-free.
func CompareTaskIDs(a, b string) int {
	ak, an, aok := splitTaskID(a)
	bk, bn, bok := splitTaskID(b)
	if aok != bok {
		if aok {
			return -1 // well-formed sorts before malformed
		}
		return 1
	}
	if !aok {
		return strings.Compare(a, b) // both malformed: raw lexical
	}
	if ak != bk {
		return strings.Compare(ak, bk)
	}
	return cmp.Compare(an, bn)
}

// SortTaskIDs sorts task ids in place by CompareTaskIDs.
func SortTaskIDs(ids []string) {
	slices.SortFunc(ids, CompareTaskIDs)
}
