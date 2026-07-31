package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// opusRates mirrors the seeded claude-opus-5 standard row: $5 / $25 per MTok,
// with the 1.25x / 2x / 0.1x cache classes spelled out.
var opusRates = ModelPrice{
	Model:              "claude-opus-5",
	Speed:              SpeedStandard,
	Currency:           "USD",
	InputMicros:        5_000_000,
	CacheWrite5mMicros: 6_250_000,
	CacheWrite1hMicros: 10_000_000,
	CacheReadMicros:    500_000,
	OutputMicros:       25_000_000,
}

// sixFractionDigits matches the shape every amount this package emits must
// have, so numeric(_,6) round-trips it.
var sixFractionDigits = regexp.MustCompile(`^\d+\.\d{6}$`)

func TestModelPriceCost(t *testing.T) {
	// A price whose five rates are equal and tiny: each class alone rounds to
	// nothing, so the case only comes out right if the products are summed
	// before the single divide.
	tinyFlat := ModelPrice{
		InputMicros: 100_000, CacheWrite5mMicros: 100_000, CacheWrite1hMicros: 100_000,
		CacheReadMicros: 100_000, OutputMicros: 100_000,
	}

	tests := []struct {
		name   string
		price  ModelPrice
		tokens TokenCounts
		want   string
		why    string
	}{
		{
			name:  "all five classes at once",
			price: opusRates,
			tokens: TokenCounts{
				Input: 1_000, CacheWrite5m: 2_000, CacheWrite1h: 3_000,
				CacheRead: 4_000, Output: 5_000,
			},
			// 1000*5 + 2000*6.25 + 3000*10 + 4000*0.5 + 5000*25 = 174,500 micros
			want: "0.174500",
		},
		{
			name:   "cache read is a tenth of base input, not base input",
			price:  opusRates,
			tokens: TokenCounts{CacheRead: 1_000_000},
			want:   "0.500000",
			why:    "pricing a cache read at the base input rate would give 5.000000",
		},
		{
			name:   "1h cache write is double base input",
			price:  opusRates,
			tokens: TokenCounts{CacheWrite1h: 1_000_000},
			want:   "10.000000",
			why:    "pricing a 1h cache write at the base input rate would give 5.000000",
		},
		{
			name:   "5m cache write is 1.25x base input",
			price:  opusRates,
			tokens: TokenCounts{CacheWrite5m: 1_000_000},
			want:   "6.250000",
		},
		{
			name: "five sub-micro products round once, not per class",
			// Each class contributes 0.3 micro-units; rounding per class gives
			// 0.000000, rounding the 1.5 micro-unit sum once gives 0.000002.
			price:  tinyFlat,
			tokens: TokenCounts{Input: 3, CacheWrite5m: 3, CacheWrite1h: 3, CacheRead: 3, Output: 3},
			want:   "0.000002",
		},
		{
			name:   "exactly half a micro-unit rounds up",
			price:  ModelPrice{InputMicros: 1_500_000},
			tokens: TokenCounts{Input: 1},
			want:   "0.000002",
		},
		{
			name:   "half of one micro-unit rounds up to one",
			price:  ModelPrice{InputMicros: 500_000},
			tokens: TokenCounts{Input: 1},
			want:   "0.000001",
		},
		{
			name:   "just under half rounds down",
			price:  ModelPrice{InputMicros: 499_999},
			tokens: TokenCounts{Input: 1},
			want:   "0.000000",
		},
		{
			name:   "no tokens",
			price:  opusRates,
			tokens: TokenCounts{},
			want:   "0.000000",
		},
		{
			name:   "amount above one keeps six fractional digits",
			price:  ModelPrice{InputMicros: 10_880_324},
			tokens: TokenCounts{Input: 1_000_000},
			want:   "10.880324",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.price.Cost(tt.tokens)
			if got != tt.want {
				msg := ""
				if tt.why != "" {
					msg = " (" + tt.why + ")"
				}
				t.Fatalf("Cost: got %s, want %s%s", got, tt.want, msg)
			}
			if !sixFractionDigits.MatchString(got) {
				t.Fatalf("Cost format: got %q, want exactly six fractional digits", got)
			}
		})
	}
}

func TestFormatMicrosParseMicrosRoundTrip(t *testing.T) {
	amounts := []string{
		"0.000000",
		"0.000001",
		"0.000500",
		"0.123456",
		"0.999999",
		"1.000000",
		"10.880324",
		"999999.999999",
	}
	for _, want := range amounts {
		t.Run(want, func(t *testing.T) {
			parsed, err := parseMicros(want)
			if err != nil {
				t.Fatalf("parseMicros(%q): %v", want, err)
			}
			if got := formatMicros(parsed); got != want {
				t.Fatalf("round trip: got %q, want %q", got, want)
			}
		})
	}
}

func TestParseMicrosAcceptsShortForms(t *testing.T) {
	// Postgres renders numeric(_,6) with all six digits, but a hand-written
	// rate or a trimmed value must still parse to the same micro-units.
	tests := []struct {
		in   string
		want string
	}{
		{"5", "5.000000"},
		{"0", "0.000000"},
		{"0.5", "0.500000"},
		{"0.05", "0.050000"},
		{"12.3", "12.300000"},
		{"  1.25  ", "1.250000"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			parsed, err := parseMicros(tt.in)
			if err != nil {
				t.Fatalf("parseMicros(%q): %v", tt.in, err)
			}
			if got := formatMicros(parsed); got != tt.want {
				t.Fatalf("parseMicros(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseMicrosRejectsBadInput(t *testing.T) {
	// Anything the micro-unit representation cannot hold exactly is an error
	// rather than a silent truncation to zero.
	for _, in := range []string{"0.1234567", "abc", "1.2.3", "1,5", "12 34", "1e6"} {
		t.Run(in, func(t *testing.T) {
			if _, err := parseMicros(in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("parseMicros(%q): got %v, want ErrInvalidInput", in, err)
			}
		})
	}
}

func TestMicroAmountSumsExactly(t *testing.T) {
	// A float64 accumulator drifts on all three of these: 0.1+0.2 is not 0.3,
	// ten 0.07s are not 0.7, and a thousand 0.001s are not 1.
	thousandth := make([]string, 1000)
	for i := range thousandth {
		thousandth[i] = "0.001000"
	}
	tenSevenths := make([]string, 10)
	for i := range tenSevenths {
		tenSevenths[i] = "0.070000"
	}

	tests := []struct {
		name    string
		amounts []string
		want    string
	}{
		{name: "zero value", want: "0.000000"},
		{name: "classic float trap", amounts: []string{"0.100000", "0.200000"}, want: "0.300000"},
		{name: "ten sevenths of a cent", amounts: tenSevenths, want: "0.700000"},
		{name: "a thousand thousandths", amounts: thousandth, want: "1.000000"},
		{
			name:    "many small agent turns",
			amounts: []string{"0.000123", "0.004567", "0.000009", "1.234567", "0.000001"},
			want:    "1.239267",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m microAmount
			for _, a := range tt.amounts {
				if err := m.add(a); err != nil {
					t.Fatalf("add(%q): %v", a, err)
				}
			}
			if got := m.String(); got != tt.want {
				t.Fatalf("sum: got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMicroAmountRejectsBadAmount(t *testing.T) {
	var m microAmount
	if err := m.add("not-a-number"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("add of garbage: got %v, want ErrInvalidInput", err)
	}
}

func TestNormalizeSpeed(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", SpeedStandard}, // vendor omits the field on a standard turn
		{"standard", SpeedStandard},
		{"fast", SpeedFast},
		{"FAST", SpeedStandard}, // unrecognized: never bill fast rates by accident
		{"turbo", SpeedStandard},
	}
	for _, tt := range tests {
		t.Run("speed="+tt.in, func(t *testing.T) {
			if got := NormalizeSpeed(tt.in); got != tt.want {
				t.Fatalf("NormalizeSpeed(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// day is a UTC calendar day, the granularity a rate lookup takes.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestModelPriceForEffectiveDating asserts against the two seeded
// claude-sonnet-5 rows: introductory $2/$10 until 2026-09-01, $3/$15 after.
// A July session must not be repriced by a September rate.
func TestModelPriceForEffectiveDating(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	tests := []struct {
		name          string
		day           time.Time
		wantInput     int64
		wantOutput    int64
		wantEffective string
	}{
		{"day before the change", day(2026, 8, 31), 2_000_000, 10_000_000, "2000-01-01"},
		{"the day of the change", day(2026, 9, 1), 3_000_000, 15_000_000, "2026-09-01"},
		{"well after the change", day(2026, 12, 1), 3_000_000, 15_000_000, "2026-09-01"},
		{"long before either", day(2026, 1, 2), 2_000_000, 10_000_000, "2000-01-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := s.ModelPriceFor(ctx, "claude-sonnet-5", "standard", tt.day)
			if err != nil {
				t.Fatalf("ModelPriceFor: %v", err)
			}
			if p == nil {
				t.Fatalf("ModelPriceFor on %s: got nil, want a rate", tt.day.Format(time.DateOnly))
			}
			if p.InputMicros != tt.wantInput {
				t.Fatalf("input_micros: got %d, want %d", p.InputMicros, tt.wantInput)
			}
			if p.OutputMicros != tt.wantOutput {
				t.Fatalf("output_micros: got %d, want %d", p.OutputMicros, tt.wantOutput)
			}
			if got := p.EffectiveFrom.UTC().Format(time.DateOnly); got != tt.wantEffective {
				t.Fatalf("effective_from: got %s, want %s", got, tt.wantEffective)
			}
		})
	}
}

func TestModelPriceForDatedSnapshotFallsBackToAlias(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	p, err := s.ModelPriceFor(ctx, "claude-haiku-4-5-20251001", "standard", day(2026, 7, 19))
	if err != nil {
		t.Fatalf("ModelPriceFor: %v", err)
	}
	if p == nil {
		t.Fatal("dated snapshot id: got nil, want the alias's rate")
	}
	if p.InputMicros != 1_000_000 || p.OutputMicros != 5_000_000 {
		t.Fatalf("rates: got in=%d out=%d, want in=1000000 out=5000000", p.InputMicros, p.OutputMicros)
	}
	// The caller asked about the snapshot id, so that is what comes back.
	if p.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("model: got %q, want %q", p.Model, "claude-haiku-4-5-20251001")
	}
}

func TestModelPriceForUnknownIsNilNotZero(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	// A zero price would silently bill an unknown model at nothing; nil makes
	// it a visible coverage gap instead.
	for _, model := range []string{"gpt-9", "", "claude-haiku-4-5-2025"} {
		t.Run("model="+model, func(t *testing.T) {
			p, err := s.ModelPriceFor(ctx, model, "standard", day(2026, 7, 19))
			if err != nil {
				t.Fatalf("ModelPriceFor(%q): %v", model, err)
			}
			if p != nil {
				t.Fatalf("ModelPriceFor(%q): got %+v, want nil", model, p)
			}
		})
	}
}

func TestModelPriceForSpeedIsASeparateSKU(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	d := day(2026, 7, 19)

	standard, err := s.ModelPriceFor(ctx, "claude-opus-5", "standard", d)
	if err != nil || standard == nil {
		t.Fatalf("standard rate: %+v, %v", standard, err)
	}
	fast, err := s.ModelPriceFor(ctx, "claude-opus-5", "fast", d)
	if err != nil || fast == nil {
		t.Fatalf("fast rate: %+v, %v", fast, err)
	}
	if standard.InputMicros != 5_000_000 || fast.InputMicros != 10_000_000 {
		t.Fatalf("input_micros: got standard=%d fast=%d, want 5000000 and 10000000",
			standard.InputMicros, fast.InputMicros)
	}

	// An unrecognized speed normalizes to standard rather than missing.
	turbo, err := s.ModelPriceFor(ctx, "claude-opus-5", "turbo", d)
	if err != nil || turbo == nil {
		t.Fatalf("unrecognized speed: %+v, %v", turbo, err)
	}
	if turbo.InputMicros != standard.InputMicros {
		t.Fatalf("unrecognized speed: got %d, want the standard %d",
			turbo.InputMicros, standard.InputMicros)
	}

	// A model with no fast row on file is unpriced at fast, not silently
	// priced at its standard rate.
	haikuFast, err := s.ModelPriceFor(ctx, "claude-haiku-4-5", "fast", d)
	if err != nil {
		t.Fatalf("haiku fast: %v", err)
	}
	if haikuFast != nil {
		t.Fatalf("haiku fast: got %+v, want nil", haikuFast)
	}
}

func TestUpsertModelPrice(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	from := day(2026, 7, 1)

	p := ModelPrice{
		Model: "vendor-x-1", EffectiveFrom: from,
		InputMicros: 1_000_000, CacheWrite5mMicros: 1_250_000,
		CacheWrite1hMicros: 2_000_000, CacheReadMicros: 100_000, OutputMicros: 5_000_000,
	}
	if err := s.UpsertModelPrice(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.ModelPriceFor(ctx, "vendor-x-1", "", day(2026, 7, 19))
	if err != nil || got == nil {
		t.Fatalf("read back: %+v, %v", got, err)
	}
	if got.InputMicros != 1_000_000 {
		t.Fatalf("input_micros: got %d, want 1000000", got.InputMicros)
	}
	// An empty speed and currency take the standard SKU and USD.
	if got.Speed != SpeedStandard || got.Currency != "USD" {
		t.Fatalf("defaults: got speed=%q currency=%q, want %q and USD", got.Speed, got.Currency, SpeedStandard)
	}

	// Same (model, speed, effective_from): a correction overwrites rather than
	// adding a second row.
	p.InputMicros = 7_000_000
	p.OutputMicros = 35_000_000
	if err := s.UpsertModelPrice(ctx, p); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, err = s.ModelPriceFor(ctx, "vendor-x-1", "standard", day(2026, 7, 19))
	if err != nil || got == nil {
		t.Fatalf("read back after overwrite: %+v, %v", got, err)
	}
	if got.InputMicros != 7_000_000 || got.OutputMicros != 35_000_000 {
		t.Fatalf("rates after overwrite: got in=%d out=%d, want 7000000 and 35000000",
			got.InputMicros, got.OutputMicros)
	}
	var rows int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM model_prices WHERE model = 'vendor-x-1'`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("model_prices rows for vendor-x-1: got %d, want 1", rows)
	}

	// A later effective_from is a second row, not an overwrite.
	p.EffectiveFrom = day(2026, 9, 1)
	p.InputMicros = 9_000_000
	if err := s.UpsertModelPrice(ctx, p); err != nil {
		t.Fatalf("second dated row: %v", err)
	}
	before, err := s.ModelPriceFor(ctx, "vendor-x-1", "standard", day(2026, 8, 31))
	if err != nil || before == nil {
		t.Fatalf("rate before the change: %+v, %v", before, err)
	}
	if before.InputMicros != 7_000_000 {
		t.Fatalf("rate before the change: got %d, want 7000000", before.InputMicros)
	}
	after, err := s.ModelPriceFor(ctx, "vendor-x-1", "standard", day(2026, 9, 1))
	if err != nil || after == nil {
		t.Fatalf("rate on the change day: %+v, %v", after, err)
	}
	if after.InputMicros != 9_000_000 {
		t.Fatalf("rate on the change day: got %d, want 9000000", after.InputMicros)
	}
}

func TestUpsertModelPriceValidation(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	valid := ModelPrice{Model: "vendor-x-2", EffectiveFrom: day(2026, 7, 1)}

	tests := []struct {
		name  string
		price ModelPrice
	}{
		{"bad currency", func() ModelPrice { p := valid; p.Currency = "dollars"; return p }()},
		{"lowercase currency", func() ModelPrice { p := valid; p.Currency = "usd"; return p }()},
		{"no model", func() ModelPrice { p := valid; p.Model = ""; return p }()},
		{"no effective_from", func() ModelPrice { p := valid; p.EffectiveFrom = time.Time{}; return p }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.UpsertModelPrice(ctx, tt.price); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}
		})
	}
}

// Total is the sum of all five classes — the coverage figure, never a
// billable quantity.
func TestTokenCountsTotalAndAdd(t *testing.T) {
	a := TokenCounts{Input: 1, CacheWrite5m: 2, CacheWrite1h: 4, CacheRead: 8, Output: 16}
	if got := a.Total(); got != 31 {
		t.Fatalf("Total: got %d, want 31", got)
	}
	a.Add(TokenCounts{Input: 1, CacheWrite5m: 1, CacheWrite1h: 1, CacheRead: 1, Output: 1})
	want := TokenCounts{Input: 2, CacheWrite5m: 3, CacheWrite1h: 5, CacheRead: 9, Output: 17}
	if a != want {
		t.Fatalf("Add: got %+v, want %+v", a, want)
	}
}

// Every seeded per-class rate must be the documented 1.25x / 2x / 0.1x
// multiple of its base input rate. The failure this catches is an operator
// editing one class by hand and misplacing a zero.
func TestSeededModelPricesFollowCacheMultipliers(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.db.Query(
		`SELECT model, speed, effective_from, input_micros, cache_write_5m_micros,
		        cache_write_1h_micros, cache_read_micros
		   FROM model_prices ORDER BY model, speed, effective_from`)
	if err != nil {
		t.Fatalf("read model_prices: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var model, speed string
		var from time.Time
		var in, w5m, w1h, read int64
		if err := rows.Scan(&model, &speed, &from, &in, &w5m, &w1h, &read); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		id := fmt.Sprintf("%s/%s@%s", model, speed, from.Format(time.DateOnly))
		if w5m*4 != in*5 { // 1.25x
			t.Errorf("%s: cache_write_5m_micros %d is not 1.25x input %d", id, w5m, in)
		}
		if w1h != in*2 {
			t.Errorf("%s: cache_write_1h_micros %d is not 2x input %d", id, w1h, in)
		}
		if read*10 != in {
			t.Errorf("%s: cache_read_micros %d is not 0.1x input %d", id, read, in)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen == 0 {
		t.Fatal("no seeded rates found in model_prices")
	}
}

// TestFormatMicrosPadsBelowOne pins the leading-zero padding path directly:
// an amount under 1.0 has fewer digits than the fraction needs.
func TestFormatMicrosPadsBelowOne(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"0.000001", "0.000001"},
		{"0.010000", "0.010000"},
		{"0.100000", "0.100000"},
	} {
		parsed, err := parseMicros(tt.in)
		if err != nil {
			t.Fatalf("parseMicros(%q): %v", tt.in, err)
		}
		got := formatMicros(parsed)
		if got != tt.want {
			t.Fatalf("formatMicros(%q): got %q, want %q", tt.in, got, tt.want)
		}
		if !strings.HasPrefix(got, "0.") {
			t.Fatalf("formatMicros(%q): got %q, want a leading zero", tt.in, got)
		}
	}
}
