
At startup, before the indexer embeds anything, the server compares the configured provider's `ID()` against `embedding_config.provider_id`. On a mismatch it nulls every stored vector and records the new id. Invalidation nulls `embedding` without touching `content_hash`, so the staleness query also treats a chunk with no vector as stale when a provider is configured, and as finished work when none is. During a re-embed the instance degrades to lexical-only. Vectors from two models are never mixed in one column.

