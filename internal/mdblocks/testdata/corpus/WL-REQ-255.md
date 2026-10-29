
The embedding width is fixed at 768 dimensions for every provider. This keeps vectors indexable (pgvector HNSW indexes only up to 2000 dimensions), lets a model swap re-embed without a schema change, and makes the typmod an INSERT-time guard. All supported models are Matryoshka-trained, so truncation yields a real embedding.

| Provider | Model | Width knob |
|---|---|---|
| Default, self-hosted CPU | `google/embeddinggemma-300m` via a `text-embeddings-inference` sidecar with OpenAI-compatible `/v1/embeddings` | native 768 |
| OpenAI | `text-embedding-3-small` | `dimensions: 768` |
| Google | `gemini-embedding-001` | `output_dimensionality: 768`, renormalise; native 3072 is unindexable |

The default runs on CPU with no GPU and no third-party call, because the corpus is the org's unreleased written record. A fourth provider must reach exactly 768 and speak an OpenAI-compatible endpoint or implement `embed.Provider`. Anthropic serves no embeddings API.

