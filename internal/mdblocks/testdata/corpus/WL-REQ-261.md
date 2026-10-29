
| Surface | Contract |
|---|---|
| `GET /api/v1/search?q=&kind=&project=&limit=&mode=` | ranked `model.SearchHit` values: `Kind`, subject id, `Anchor`, `Title`, excerpt from `chunk_text`, fused score, and the per-arm ranks, plus the response's `provider` and effective `mode`. `kind` is repeatable; omitted means all three. `mode` is `hybrid` (default), `dense` or `lexical` |
| `lode search <query> [--kind doc|task|skill] [--mode] [--limit] [--json]` | renders `WL-SPEC-75 §9.3  0.032  Total order and the commit horizon` |
| `POST /api/v1/skills/recommend` | a thin caller of the same path with `kind=skill`, so it uses both arms |

The cockpit search box is described in WL-SPEC-82.

