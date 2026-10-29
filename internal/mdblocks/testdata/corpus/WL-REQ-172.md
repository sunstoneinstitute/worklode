
**Token budget.** A plan body is measured against a token budget: the server's soft and hard values come from `LODE_PLAN_TOKENS_SOFT` and `LODE_PLAN_TOKENS_HARD` (defaults 32000 and 64000), a project may override either under `plan_tokens_soft` and `plan_tokens_hard` in its settings, a write over the soft budget carries a warning and a write over the hard ceiling is refused. The estimate is runes × 4 / 7, the inverse of the corpus index's ratio of 7 runes to 4 tokens. Index chunk size itself follows the embedding model's window, `LODE_EMBEDDING_CONTEXT_TOKENS` (WL-SPEC-79 §14.4).

