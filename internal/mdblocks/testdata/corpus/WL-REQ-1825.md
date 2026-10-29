
| Metric | Labels | Values |
|----|----|----|
| `worklode_mcp_tool_calls_total` | `tool`, `outcome` | outcome: `ok`, `invalid`, `project_required`, `confirm_project`, `not_found`, `refused`, `conflict`, `error` |
| `worklode_mcp_oauth_requests_total` | `endpoint`, `outcome` | endpoint: `metadata`, `authorize`, `callback`, `consent`, `token`. outcome: `ok`, `not_found`, or the OAuth error code |

The client, the redirect URI and the actor are never labels, so label values stay bounded.

