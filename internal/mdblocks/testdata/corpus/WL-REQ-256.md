
```go
type Role int // RoleDocument | RoleQuery

type Provider interface {
    Embed(ctx context.Context, role Role, texts []string) ([][]float32, error)
    ID() string
    Dim() int // must be 768; NewServer refuses a provider that disagrees
}
```

`NewServer` builds an `embed.OpenAI` provider from `LODE_EMBEDDING_URL`, `LODE_EMBEDDING_MODEL` (required with the URL) and `LODE_EMBEDDING_API_KEY`. Queries and documents are different inputs: EmbeddingGemma expects `task: search result | query: ` and `title: none | text: ` prefixes, Gemini a `task_type`. `OpenAI` carries `QueryPrefix`, `DocumentPrefix` (`LODE_EMBEDDING_QUERY_PREFIX`, `LODE_EMBEDDING_DOCUMENT_PREFIX`) and a `Dimensions` request field; a symmetric model sets both prefixes empty.

