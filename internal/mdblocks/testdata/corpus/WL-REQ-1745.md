
| Condition | Behaviour |
|---|---|
| No embedding provider | Pins plus lexical matches, `provider: none`. |
| Provider down or slower than 2 s | Pins plus lexical matches and a warning. Never blocks a claim. |
| Search fails | Pins, no matches, a warning. |
| Archive fetch fails | Recommended skills still listed with an install hint; pinned content unaffected. |
| Skill removed from git | Soft-deleted, never recommended; existing pins still resolve, with a "removed from its source repo" warning. |


