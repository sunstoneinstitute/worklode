// This file holds only shared rendering helpers used by more than one
// feature; each feature's own *Table/*Render functions live in that
// feature's file (tasks.go, docs.go, ...).
package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newTabwriter returns a tabwriter configured the same way for every table
// in this package: 2 spaces of padding between columns.
func newTabwriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// dash renders an unset string as the "-" every view uses for "not set".
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// DocNumber renders a document's number, or "-" for a plan, which carries
// none (025 §14.3).
func DocNumber(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// DocRef is a document's formatted id: <KEY>-<KIND>-<N>, the 025 §14.3
// shorthand as widened by 029 §4 — "WL-SPEC-29", "WL-ADR-43", "WL-PLAN-7".
// It is what a person cites, which the integer id and the bare corpus number
// are not, so it replaces both wherever a document lists.
//
// Every kind takes the same form, plans included. They were the exception
// while 025 §14.3 gave them no number; 029 §4 puts them on their project's
// sequence, so nothing here special-cases a kind.
//
// An unknown project key degrades to the unqualified "SPEC-29" rather than
// guessing one or printing a leading dash: the number is still true, and only
// the corpus it is scoped to went missing. A document with no number at all
// predates 029 §4's backfill and renders as its kind, which is what the whole
// column said for plans before.
func DocRef(d model.Doc) string {
	if d.Number == 0 {
		return d.Kind
	}
	ref := strings.ToUpper(d.Kind) + "-" + strconv.Itoa(d.Number)
	if d.ProjectKey == "" {
		return ref
	}
	return d.ProjectKey + "-" + ref
}

// LocalTime formats t in the local zone, or "-" for the zero value. Every
// timestamp the CLI prints goes through it, including the one-line
// confirmations in internal/cmd, so a lease expiry reads the same wherever it
// appears.
func LocalTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

// HumanTokens abbreviates a token count for a table cell: 1.2k, 11.8M. Token
// counts run to eight digits in an agentic session, where the exact figure is
// noise and the magnitude is the point.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// Money renders a stored decimal amount for reading, at two decimal
// places. See model.Money for the rounding rule.
func Money(amount string) string {
	return model.Money(amount)
}

// KeySuffix renders " (WL)" for a known task-id key, or nothing.
func KeySuffix(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// CostRender writes one block per currency: a headline total, a row per day,
// and — when some tokens were billed on a model with no price on file — the
// shortfall that headline therefore omits.
func CostRender(w io.Writer, cost model.CostReport, window string) {
	if len(cost.Totals) == 0 {
		fmt.Fprintf(w, "\ncost, %s: none recorded\n", window)
		return
	}
	// No currency symbol: a vendor need not bill in dollars, and one block per
	// currency already names it in the header. "$12.000000 EUR" is the kind of
	// wrong a symbol table earns you.
	for _, total := range cost.Totals {
		fmt.Fprintf(w, "\ncost, %s: %s %s\n", window, Money(total.CostAmount), total.Currency)
		tw := newTabwriter(w)
		for _, d := range cost.Days {
			if d.Currency != total.Currency {
				continue
			}
			fmt.Fprintf(tw, "  %s\t%s\tin %s\tcache-w %s\tcache-r %s\tout %s\n",
				d.Day, Money(d.CostAmount),
				HumanTokens(d.InputTokens),
				HumanTokens(d.CacheWrite5mTokens+d.CacheWrite1hTokens),
				HumanTokens(d.CacheReadTokens),
				HumanTokens(d.OutputTokens))
		}
		tw.Flush()
		if total.UnpricedTokens > 0 {
			fmt.Fprintf(w, "note: %s tokens from models with no price on file are excluded from the total.\n",
				HumanTokens(total.UnpricedTokens))
		}
	}
}
