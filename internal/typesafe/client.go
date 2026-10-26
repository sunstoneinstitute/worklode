// Package typesafe calls TypeSafe's System One API, which the jev classifier
// uses to pick an inline reference's predicate (WL-REQ-1857). Request and
// answer shapes follow https://docs.typesafe.ai/api.md.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the TypeSafe API host.
const DefaultBaseURL = "https://api.typesafe.ai"

// ErrUnconfigured is returned by Ask when the client has no API key.
var ErrUnconfigured = errors.New("typesafe: no API key configured")

// Client calls POST /v1/systemone. An empty BaseURL means DefaultBaseURL, a
// nil HTTP means http.DefaultClient, and a nil Metrics records nothing.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	Metrics *Metrics
}

// Question is one System One question. Type is "choice" for the jev
// classifier; Criteria maps each option name to its description.
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// Answer is a Choice answer: the chosen option, its confidence, and the
// probability of every option.
type Answer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// Ask sends questions about state to model in one request and returns the
// answers under the same question ids.
func (c *Client) Ask(ctx context.Context, model string, state any, questions map[string]Question) (map[string]Answer, error) {
	if c.APIKey == "" {
		c.Metrics.observe("unconfigured", 0)
		return nil, ErrUnconfigured
	}
	start := time.Now()
	answers, err := c.ask(ctx, model, state, questions)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	c.Metrics.observe(outcome, time.Since(start))
	return answers, err
}

func (c *Client) ask(ctx context.Context, model string, state any, questions map[string]Question) (map[string]Answer, error) {
	body, err := json.Marshal(struct {
		Model     string              `json:"model"`
		State     any                 `json:"state"`
		Questions map[string]Question `json:"questions"`
	}{model, state, questions})
	if err != nil {
		return nil, fmt.Errorf("typesafe: encode request: %w", err)
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("typesafe: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("typesafe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("typesafe: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Answers map[string]Answer `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("typesafe: decode response: %w", err)
	}
	for id := range questions {
		if _, ok := out.Answers[id]; !ok {
			return nil, fmt.Errorf("typesafe: no answer for question %q", id)
		}
	}
	return out.Answers, nil
}
