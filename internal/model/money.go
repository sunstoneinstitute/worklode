package model

import (
	"fmt"
	"strconv"
	"strings"
)

// Money renders a stored decimal amount for reading, at two decimal places.
// Amounts are stored and summed at micro-unit precision because per-token
// rates need it — a token of cache read costs half a millionth of a dollar —
// but nobody reads a bill in millionths.
//
// A nonzero amount below half a cent renders as "<0.01": rounding real spend
// down to "0.00" would report it as free. Rounding is half-up, so the total
// line and the day lines can disagree by a cent; that is the honest cost of
// showing rounded components and a total that was summed unrounded.
func Money(amount string) string {
	whole, frac, _ := strings.Cut(strings.TrimSpace(amount), ".")
	if whole == "" {
		whole = "0"
	}
	for len(frac) < 3 {
		frac += "0"
	}
	units, err := strconv.ParseInt(whole+frac[:2], 10, 64)
	if err != nil {
		return amount // not a decimal we understand; show it verbatim
	}
	if frac[2] >= '5' {
		units++
	}
	if units == 0 && strings.ContainsAny(whole+frac, "123456789") {
		return "<0.01"
	}
	return fmt.Sprintf("%d.%02d", units/100, units%100)
}
