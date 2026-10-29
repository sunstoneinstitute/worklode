
An instance with no embedding provider still has working search: convergence chunks and writes `tsv`, the lexical arm runs alone, and the response reports `provider: "none"` and `mode: "lexical"`. A failing provider degrades the same way and retries next pass. A broken `tsv` degrades to dense-only. Neither arm's failure takes search down.

