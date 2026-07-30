// Package embed abstracts text-embedding computation behind a small provider
// interface. The server holds the only credentials; agents never embed.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Provider computes one vector per input text, order-preserving.
type Provider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// ID identifies the embedding space (provider + model). Vectors from
	// different IDs are not comparable, so a change invalidates every stored
	// embedding.
	ID() string
}

// Chunk sizing for SKILL.md bodies and recommend-query text, in runes.
// 6000 runes is roughly 1500 tokens of English, comfortably inside the
// 8k-token window shared by the OpenAI-family embedding models; the overlap
// keeps boundary-spanning matches findable.
const (
	ChunkRunes   = 6000
	ChunkOverlap = 600
)

// Chunks splits s into overlapping chunks of at most size runes.
func Chunks(s string, size, overlap int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if size <= 0 {
		size = ChunkRunes
	}
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	step := size - overlap
	var out []string
	for start := 0; ; start += step {
		end := start + size
		if end >= len(r) {
			out = append(out, string(r[start:]))
			return out
		}
		out = append(out, string(r[start:end]))
	}
}

// Truncate returns at most n leading runes of s.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// OpenAI calls an OpenAI-compatible embeddings endpoint (the full URL,
// e.g. https://api.example.com/v1/embeddings).
type OpenAI struct {
	URL   string // full endpoint, e.g. https://api.openai.com/v1/embeddings
	Model string
	Key   string
	// HTTPClient overrides the default 30s-timeout client.
	HTTPClient *http.Client
}

// ID identifies this provider's embedding space as model+endpoint, e.g.
// "openai:text-embedding-3-small@api.openai.com/v1/embeddings". The path is
// included because a path-routed gateway (LiteLLM, vLLM, text-embeddings-
// inference behind a prefix) can serve different backends from one host.
func (p *OpenAI) ID() string {
	endpoint := p.URL
	if u, err := url.Parse(p.URL); err == nil {
		// Host+Path identifies the endpoint. Userinfo, query, and fragment are
		// excluded so a key passed in the URL can never reach the stored ID.
		if e := u.Host + u.Path; e != "" {
			endpoint = e
		}
	}
	return fmt.Sprintf("openai:%s@%s", p.Model, endpoint)
}

func (p *OpenAI) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (p *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("post embeddings to %s: %w", p.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embed: %s returned %d: %s", p.URL, resp.StatusCode, msg)
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		// Duplicated, negative, or out-of-range indices would otherwise pass
		// the count check above and get mapped to the wrong input text.
		if d.Index != i {
			return nil, fmt.Errorf("embed: response indices are not a permutation of [0,%d): got index %d at position %d", len(texts), d.Index, i)
		}
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embed: empty embedding vector at index %d", d.Index)
		}
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
