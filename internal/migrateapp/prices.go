package migrateapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DefaultPricesURL is LiteLLM's community-maintained price list, the only
// machine-readable feed that covers both Anthropic and OpenAI.
const DefaultPricesURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// sinceLaunch dates a newly filed model's rate, matching the seed rows: the
// rate applies to every session recorded against the model.
var sinceLaunch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type litellmEntry struct {
	Provider       string   `json:"litellm_provider"`
	Mode           string   `json:"mode"`
	Input          *float64 `json:"input_cost_per_token"`
	Output         *float64 `json:"output_cost_per_token"`
	CacheWrite5m   *float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h   *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheRead      *float64 `json:"cache_read_input_token_cost"`
	ProviderPrices struct {
		Fast float64 `json:"fast"`
	} `json:"provider_specific_entry"`
}

// parseLiteLLM turns LiteLLM's price file into rows for Anthropic and OpenAI
// chat models. A missing cache rate falls back to the next cheaper class the
// vendor actually bills at (OpenAI has no cache-write surcharge). A "fast"
// multiplier yields a fast-speed row.
func parseLiteLLM(r io.Reader) ([]store.ModelPrice, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode price list: %w", err)
	}
	var out []store.ModelPrice
	for model, msg := range raw {
		var e litellmEntry
		if strings.Contains(model, "/") || json.Unmarshal(msg, &e) != nil {
			continue
		}
		if (e.Provider != "anthropic" && e.Provider != "openai") ||
			(e.Mode != "chat" && e.Mode != "responses") ||
			e.Input == nil || e.Output == nil {
			continue
		}
		or := func(p *float64, fallback float64) float64 {
			if p != nil {
				return *p
			}
			return fallback
		}
		w5 := or(e.CacheWrite5m, *e.Input)
		rates := [5]float64{*e.Input, w5, or(e.CacheWrite1h, w5), or(e.CacheRead, *e.Input), *e.Output}
		out = append(out, priceRow(model, store.SpeedStandard, rates, 1))
		if e.ProviderPrices.Fast > 0 {
			out = append(out, priceRow(model, store.SpeedFast, rates, e.ProviderPrices.Fast))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Speed > out[j].Speed
	})
	return out, nil
}

// priceRow converts per-token rates to micro-units per million tokens.
func priceRow(model, speed string, perToken [5]float64, mult float64) store.ModelPrice {
	m := func(i int) int64 { return int64(math.Round(perToken[i] * mult * 1e12)) }
	return store.ModelPrice{
		Model: model, Speed: speed, EffectiveFrom: sinceLaunch, Currency: "USD",
		InputMicros: m(0), CacheWrite5mMicros: m(1), CacheWrite1hMicros: m(2),
		CacheReadMicros: m(3), OutputMicros: m(4),
	}
}

// SyncPrices makes model_prices agree with the feed. A model/speed with no
// rate on file gets one dated sinceLaunch; one whose current rate differs gets
// a new row effective today, so usage already recorded keeps its price.
func SyncPrices(ctx context.Context, dsn, url string, stdout io.Writer) error {
	if dsn == "" {
		return errors.New("no DSN: set --dsn or LODE_DSN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	feed, err := parseLiteLLM(resp.Body)
	if err != nil {
		return err
	}
	s, err := store.Open(dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	return syncPrices(ctx, s, feed, time.Now(), stdout)
}

func syncPrices(ctx context.Context, s *store.Store, feed []store.ModelPrice, now time.Time, stdout io.Writer) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	added, changed := 0, 0
	for _, p := range feed {
		cur, err := s.ModelPriceFor(ctx, p.Model, p.Speed, now)
		if err != nil {
			return err
		}
		if cur == nil {
			if err := s.UpsertModelPrice(ctx, p); err != nil {
				return err
			}
			added++
			fmt.Fprintf(stdout, "added %s/%s\n", p.Model, p.Speed)
			continue
		}
		have := [5]int64{cur.InputMicros, cur.CacheWrite5mMicros, cur.CacheWrite1hMicros, cur.CacheReadMicros, cur.OutputMicros}
		want := [5]int64{p.InputMicros, p.CacheWrite5mMicros, p.CacheWrite1hMicros, p.CacheReadMicros, p.OutputMicros}
		if have != want {
			p.EffectiveFrom = today
			if err := s.UpsertModelPrice(ctx, p); err != nil {
				return err
			}
			changed++
			fmt.Fprintf(stdout, "changed %s/%s from %s: %v -> %v\n", p.Model, p.Speed, today.Format(time.DateOnly), have, want)
		}
	}
	_, err := fmt.Fprintf(stdout, "prices synced: %d added, %d changed\n", added, changed)
	return err
}
