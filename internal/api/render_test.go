package api

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The cockpit's automation-boundary card shows overhead's share of a
// project's spend, so the mapping has to carry it across (spec 052 §4).
func TestCockpitCostTotalsIncludesOverhead(t *testing.T) {
	t.Parallel()
	report := model.CostReport{Totals: []model.CostTotals{{
		Currency:   "USD",
		CostAmount: "1.500000",
		Overhead:   model.CostOverhead{CostAmount: "1.300000"},
	}}}
	got := cockpitCostTotals(report)
	if len(got) != 1 || got[0].OverheadCostAmount != "1.300000" {
		t.Fatalf("cockpitCostTotals = %+v, want OverheadCostAmount 1.300000", got)
	}
}
