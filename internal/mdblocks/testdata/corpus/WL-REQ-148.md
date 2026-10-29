
`projects.workflows jsonb NULL` and `tasks.workflow text NULL`. NULL means built-in default only, no automations. It is a JSON column because it is consulted inside the transition transaction.

```json
{
  "default": "service",
  "workflows": {
    "service": {
      "description": "PR review, then prod straight from main",
      "states": ["draft", "ready", "in_progress", "in_review", "merged", "deployed_prod", "abandoned"]
    },
    "dataset": {
      "description": "datasets are released, not deployed",
      "states": ["draft", "ready", "in_progress", "in_review", "merged", "released", "abandoned"],
      "transitions": [["merged", "released"]]
    }
  },
  "automations": []
}
```

| Key | Meaning |
|---|---|
| `default` | The name a task with no override resolves to. May name the built-in `default`. |
| `workflows` | Named definitions: `states` (must include the core) plus optional `transitions`. When present, `transitions` is the complete list of declared entries. Core edges are never listed. |
| `automations` | The ordered automation list (section 10). Optional and orthogonal to `workflows`. |

