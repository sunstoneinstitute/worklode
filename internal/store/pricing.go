package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// TokenCounts is one turn's tokens split by how they are billed. A vendor
// prices these classes at rates that differ by up to 20x, so a total is not a
// substitute for the breakdown:
//
//   - Input is the uncached remainder of the prompt only — NOT the prompt
//     size. The full prompt is Input + CacheWrite5m + CacheWrite1h + CacheRead.
//   - CacheWrite5m / CacheWrite1h are prompt prefix written to cache, billed
//     above base input (1.25x and 2x by current vendor convention) and split
//     because the two TTLs cost differently.
//   - CacheRead is prefix served from cache at a fraction of base input
//     (0.1x). It dominates volume in a long agentic session, so dropping it
//     loses most of the input bill and counting it as plain input overstates
//     that bill tenfold.
//   - Output is never cached. Last turn's output re-enters the next turn's
//     prompt and is cached there, so its tail is billed as CacheRead.
type TokenCounts struct {
	Input        int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	Output       int64
}

// Total is every billed token, for coverage reporting. It is not a quantity
// anything can be priced from.
func (t TokenCounts) Total() int64 {
	return t.Input + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead + t.Output
}

// Add accumulates other into t.
func (t *TokenCounts) Add(other TokenCounts) {
	t.Input += other.Input
	t.CacheWrite5m += other.CacheWrite5m
	t.CacheWrite1h += other.CacheWrite1h
	t.CacheRead += other.CacheRead
	t.Output += other.Output
}

// SpeedStandard and SpeedFast are the billing SKUs a model can run under.
// Fast mode is priced separately, not derived from standard.
const (
	SpeedStandard = "standard"
	SpeedFast     = "fast"
)

// NormalizeSpeed maps an absent or unrecognized speed to the standard SKU.
// A vendor payload omits the field entirely on a standard turn.
func NormalizeSpeed(speed string) string {
	if speed == SpeedFast {
		return SpeedFast
	}
	return SpeedStandard
}

// ModelPrice is one model's rates, in micro-units of Currency per million
// tokens. Integers: cost arithmetic stays exact and never touches a float.
type ModelPrice struct {
	Model              string
	Speed              string
	EffectiveFrom      time.Time
	Currency           string
	InputMicros        int64
	CacheWrite5mMicros int64
	CacheWrite1hMicros int64
	CacheReadMicros    int64
	OutputMicros       int64
}

// perMillion is the divisor implied by rates being quoted per million tokens.
var perMillion = big.NewInt(1_000_000)

// Cost prices t at p, returning a decimal string with exactly six fractional
// digits — the precision of the numeric(12,6)/numeric(14,6) columns it lands
// in, and of a micro-unit of currency.
//
// The five products are summed before the single divide, so the result is
// rounded once (half up) rather than once per class.
func (p ModelPrice) Cost(t TokenCounts) string {
	num := new(big.Int)
	scratch := new(big.Int)
	add := func(tokens, micros int64) {
		num.Add(num, scratch.Mul(big.NewInt(tokens), big.NewInt(micros)))
	}
	add(t.Input, p.InputMicros)
	add(t.CacheWrite5m, p.CacheWrite5mMicros)
	add(t.CacheWrite1h, p.CacheWrite1hMicros)
	add(t.CacheRead, p.CacheReadMicros)
	add(t.Output, p.OutputMicros)

	// Round half up to whole micro-units, then render. Tokens and rates are
	// both non-negative by CHECK constraint, so no negative-half case exists.
	num.Add(num, big.NewInt(500_000))
	num.Quo(num, perMillion)
	return formatMicros(num)
}

// formatMicros renders a whole number of micro-units as a decimal string with
// six fractional digits. Costs are never negative — tokens and rates are both
// non-negative by CHECK constraint — but the sign is handled rather than left
// to corrupt the padding, so this stays a correct decimal formatter for any
// caller that finds it.
func formatMicros(micros *big.Int) string {
	digits := micros.String()
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if len(digits) <= 6 {
		return sign + "0." + strings.Repeat("0", 6-len(digits)) + digits
	}
	return sign + digits[:len(digits)-6] + "." + digits[len(digits)-6:]
}

// microAmount sums money exactly, as a whole number of micro-units. Adding
// decimal strings by way of float64 would drift: a third of a cent is not
// representable, and an agentic workload is thousands of small amounts.
// The zero value is a usable zero.
type microAmount struct {
	micros big.Int
}

// add folds in a decimal amount with at most six fractional digits — the shape
// Cost emits and numeric(_,6) returns.
func (m *microAmount) add(amount string) error {
	parsed, err := parseMicros(amount)
	if err != nil {
		return err
	}
	m.micros.Add(&m.micros, parsed)
	return nil
}

// String renders the running total in the same six-fraction-digit form as Cost.
func (m *microAmount) String() string { return formatMicros(&m.micros) }

// parseMicros converts a decimal string to whole micro-units. It accepts the
// forms Postgres numeric(_,6) and Cost produce; anything else is an error
// rather than a silent zero.
func parseMicros(amount string) (*big.Int, error) {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return nil, fmt.Errorf("amount is empty: %w", ErrInvalidInput)
	}
	whole, frac, _ := strings.Cut(trimmed, ".")
	if len(frac) > 6 {
		return nil, fmt.Errorf("amount %q has more than six fractional digits: %w", amount, ErrInvalidInput)
	}
	digits := whole + frac + strings.Repeat("0", 6-len(frac))
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("amount %q is not a decimal number: %w", amount, ErrInvalidInput)
	}
	return n, nil
}

// datedModelSuffix matches the -YYYYMMDD snapshot suffix a vendor appends to
// a pinned model id. Prices are filed against the alias, so a snapshot id
// falls back to it rather than reading as unpriced.
var datedModelSuffix = regexp.MustCompile(`-\d{8}$`)

// queryer is the read surface shared by *sql.DB and *sql.Tx, so pricing can
// be looked up inside the transaction that records the usage it prices.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// modelPriceFor returns the rate in effect for model/speed on day, or nil if
// no rate is on file. A nil price is NOT a zero price: usage priced by it is
// recorded with a NULL amount and counted as unpriced, so an unknown model
// shows up as a coverage gap rather than as free work.
func modelPriceFor(ctx context.Context, q queryer, model, speed string, day time.Time) (*ModelPrice, error) {
	if model == "" {
		return nil, nil
	}
	speed = NormalizeSpeed(speed)
	candidates := []string{model}
	if alias := datedModelSuffix.ReplaceAllString(model, ""); alias != model {
		candidates = append(candidates, alias)
	}
	for _, candidate := range candidates {
		p, err := lookupModelPrice(ctx, q, candidate, speed, day)
		if err != nil {
			return nil, err
		}
		if p != nil {
			// Report the id we were asked about, not the alias we matched.
			p.Model = model
			return p, nil
		}
	}
	return nil, nil
}

func lookupModelPrice(ctx context.Context, q queryer, model, speed string, day time.Time) (*ModelPrice, error) {
	p := ModelPrice{Model: model, Speed: speed}
	err := q.QueryRowContext(ctx,
		`SELECT effective_from, currency, input_micros, cache_write_5m_micros,
		        cache_write_1h_micros, cache_read_micros, output_micros
		   FROM model_prices
		  WHERE model = $1 AND speed = $2 AND effective_from <= $3
		  ORDER BY effective_from DESC
		  LIMIT 1`,
		model, speed, day).
		Scan(&p.EffectiveFrom, &p.Currency, &p.InputMicros, &p.CacheWrite5mMicros,
			&p.CacheWrite1hMicros, &p.CacheReadMicros, &p.OutputMicros)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up price for %s/%s: %w", model, speed, err)
	}
	return &p, nil
}

// ModelPriceFor exposes the rate lookup for callers that want to price
// something without recording it.
func (s *Store) ModelPriceFor(ctx context.Context, model, speed string, day time.Time) (*ModelPrice, error) {
	return modelPriceFor(ctx, s.db, model, speed, day)
}

// UpsertModelPrice files a rate, replacing any row already on file for the
// same (model, speed, effective_from). Rates are operational data rather than
// code: correcting one, or filing a rate change ahead of its date, is a write
// here and needs no redeploy.
func (s *Store) UpsertModelPrice(ctx context.Context, p ModelPrice) error {
	speed := NormalizeSpeed(p.Speed)
	currency := p.Currency
	if currency == "" {
		currency = defaultCurrency
	}
	if !currencyRE.MatchString(currency) {
		return fmt.Errorf("currency %q is not an ISO 4217 code: %w", p.Currency, ErrInvalidInput)
	}
	if p.Model == "" {
		return fmt.Errorf("model is required: %w", ErrInvalidInput)
	}
	if p.EffectiveFrom.IsZero() {
		return fmt.Errorf("effective_from is required: %w", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO model_prices
		   (model, speed, effective_from, currency, input_micros,
		    cache_write_5m_micros, cache_write_1h_micros, cache_read_micros,
		    output_micros)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (model, speed, effective_from) DO UPDATE SET
		    currency              = EXCLUDED.currency,
		    input_micros          = EXCLUDED.input_micros,
		    cache_write_5m_micros = EXCLUDED.cache_write_5m_micros,
		    cache_write_1h_micros = EXCLUDED.cache_write_1h_micros,
		    cache_read_micros     = EXCLUDED.cache_read_micros,
		    output_micros         = EXCLUDED.output_micros`,
		p.Model, speed, p.EffectiveFrom.UTC(), currency, p.InputMicros,
		p.CacheWrite5mMicros, p.CacheWrite1hMicros, p.CacheReadMicros, p.OutputMicros)
	if err != nil {
		return fmt.Errorf("upsert price for %s/%s: %w", p.Model, speed, err)
	}
	return nil
}
