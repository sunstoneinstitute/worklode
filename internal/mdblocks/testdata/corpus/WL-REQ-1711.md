
The default deployment points `LODE_EMBEDDING_URL` at the `text-embeddings-inference` sidecar (`deploy/base/embeddings.yaml`). `LODE_QUERY_EMBEDDING_URL` optionally sends the three query-time embedding calls (search, skill recommend, task-brief skill matching) to a second sidecar serving the same model (`deploy/base/embeddings-query.yaml`), so a large indexer batch does not queue ahead of an interactive query. It requires `LODE_EMBEDDING_URL`, and `NewServer` refuses a query provider whose width is not 768.

Both sidecars load the fp32 weights (about 1.2 GB on disk) and need roughly
2 GiB resident each, so each pod requests at least 2 GiB of memory with a
3 GiB limit. A quantised model is what would justify a smaller request.
