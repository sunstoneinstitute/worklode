// Package embed abstracts text-embedding computation behind a small provider
// interface. The server holds the only credentials; agents never embed.
package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Role names the input mode: RoleDocument for stored content, RoleQuery for
// text a search compares against it. Retrieval-oriented models (Embedding
// Gemma, Gemini) prepend a different instruction per role; embedding a query
// with the document convention doesn't error, it silently costs retrieval
// quality.
type Role int

const (
	RoleDocument Role = iota
	RoleQuery
)

// Provider computes one vector per input text, order-preserving.
type Provider interface {
	Embed(ctx context.Context, role Role, texts []string) ([][]float32, error)
	// ID identifies the embedding space: every configured input that changes
	// what a stored vector means. Vectors from different IDs are not
	// comparable, so a change invalidates every stored embedding.
	ID() string
	// Dim returns the vector width this provider produces. It must be 768;
	// NewServer refuses a provider that disagrees.
	Dim() int
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

// DefaultMaxBatch is how many inputs one HTTP request carries when MaxBatch
// says nothing, and DefaultTimeout is how long that request may take.
//
// The timeout is fixed and the caller's input count is not: the indexer hands
// over a whole subject at once, and a long spec chunks into dozens of pieces
// (040 §4.2) — the corpus holds one of 88. On the in-cluster CPU backend an
// input costs on the order of a second, so an unsplit request for a large doc
// cannot finish inside any timeout worth setting, and the retry that follows
// it is more load on a server that is already behind. Eight inputs keeps one
// request's work bounded and still amortises the round trip; a minute is
// several times what eight should ever need.
const (
	DefaultMaxBatch = 8
	DefaultTimeout  = 60 * time.Second
)

// OpenAI calls an OpenAI-compatible embeddings endpoint (the full URL,
// e.g. https://api.example.com/v1/embeddings).
type OpenAI struct {
	URL   string // full endpoint, e.g. https://api.openai.com/v1/embeddings
	Model string
	Key   string
	// QueryPrefix and DocumentPrefix are prepended to every input text for
	// the matching Role. Both empty (the zero value) reproduces today's
	// behaviour: no prefixing, for symmetric models that don't need it.
	QueryPrefix    string
	DocumentPrefix string
	// Dimensions, when > 0, is sent as the request body's "dimensions" field
	// to truncate the model's native width. Leave it 0 for a sidecar that
	// rejects the parameter, configured with a natively-768 model instead.
	Dimensions int
	// MaxBatch caps how many inputs one HTTP request carries; Embed splits a
	// longer slice across several requests. 0 means DefaultMaxBatch.
	MaxBatch int
	// HTTPClient overrides the default DefaultTimeout client.
	HTTPClient *http.Client
	// Metrics records call outcomes and duration. Nil records nothing.
	Metrics *Metrics
}

// Dim returns the configured Dimensions, or 768 when unset — the default
// width of OpenAI's small embedding models and the index's fixed column
// width.
func (p *OpenAI) Dim() int {
	if p.Dimensions > 0 {
		return p.Dimensions
	}
	return 768
}

// ID identifies this provider's embedding space as model+width+endpoint, plus
// a digest of the role prefixes when either is set, e.g.
// "openai:text-embedding-3-small@768@api.openai.com/v1/embeddings".
// The width is included because the same model truncated to a different
// dimension is a different, incomparable space. The path is included
// because a path-routed gateway (LiteLLM, vLLM, text-embeddings-inference
// behind a prefix) can serve different backends from one host. The prefixes
// are included because text embedded under a different instruction lands
// somewhere else in the same model's space; they are digested rather than
// spelled out to keep the ID bounded, and left off entirely when both are
// empty so a symmetric instance keeps the ID it already recorded (040 §3).
func (p *OpenAI) ID() string {
	endpoint := p.URL
	if u, err := url.Parse(p.URL); err == nil {
		// Host+Path identifies the endpoint. Userinfo, query, and fragment are
		// excluded so a key passed in the URL can never reach the stored ID.
		if e := u.Host + u.Path; e != "" {
			endpoint = e
		}
	}
	id := fmt.Sprintf("openai:%s@%d@%s", p.Model, p.Dim(), endpoint)
	if p.QueryPrefix != "" || p.DocumentPrefix != "" {
		// NUL-separated so ("ab", "") and ("a", "b") cannot collide.
		sum := sha256.Sum256([]byte(p.QueryPrefix + "\x00" + p.DocumentPrefix))
		id += "#" + hex.EncodeToString(sum[:4])
	}
	return id
}

func (p *OpenAI) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// Embed sends texts in batches of at most MaxBatch inputs and concatenates
// the results in order. A batch that fails fails the whole call: the caller
// stores one subject's vectors as a set, so half of them is not a usable
// answer.
func (p *OpenAI) Embed(ctx context.Context, role Role, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	size := p.MaxBatch
	if size <= 0 {
		size = DefaultMaxBatch
	}
	vecs := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += size {
		batch := texts[start:min(start+size, len(texts))]
		began := time.Now()
		got, err := p.embed(ctx, role, batch)
		p.Metrics.observe(err, time.Since(began))
		if err != nil {
			return nil, err
		}
		vecs = append(vecs, got...)
	}
	return vecs, nil
}

func (p *OpenAI) embed(ctx context.Context, role Role, texts []string) ([][]float32, error) {
	prefix := p.DocumentPrefix
	if role == RoleQuery {
		prefix = p.QueryPrefix
	}
	input := texts
	if prefix != "" {
		input = make([]string, len(texts))
		for i, t := range texts {
			input[i] = prefix + t
		}
	}
	reqBody := map[string]any{"model": p.Model, "input": input}
	if p.Dimensions > 0 {
		reqBody["dimensions"] = p.Dimensions
	}
	body, err := json.Marshal(reqBody)
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
