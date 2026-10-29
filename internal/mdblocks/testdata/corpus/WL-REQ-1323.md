
| Route | Does |
|---|---|
| `POST /api/v1/reviews` | open; reviewers come from `doc_reviewers` or `task_reviewers` |
| `GET /api/v1/reviews?subject=&reviewer=&state=` | list |
| `GET /api/v1/reviews/{id}` | review, threads, rounds, current guide |
| `GET /api/v1/reviews/{id}/guide` | guide JSON |
| `PUT /api/v1/reviews/{id}/guide` | the author's guide proposal for the current round (WL-RULE-1320) |
| `POST /api/v1/reviews/{id}/rounds` | submit a round: a reviewer round carries a `verdict`, the author's round carries a new `base_ref` and no verdict |
| `GET /api/v1/reviews/{id}/rounds/{n}` | one round |
| `POST /api/v1/reviews/{id}/threads` | open a thread |
| `POST /api/v1/reviews/{id}/threads/{tid}/messages` | reply |
| `POST /api/v1/reviews/{id}/threads/{tid}/resolve` | resolve |
| `GET /api/v1/reviews/{id}/events?after=<seq>&timeout=` | long-poll; 204 on timeout |
| `GET /api/v1/docs/{id}/threads?version=&anchor=&status=` | a document's threads, bare notes and review threads together, `obsolete` included |
| `GET /api/v1/rules/{id}/threads?version=&status=` | a rule's threads, bare notes and review threads together, `obsolete` included |
| `POST /api/v1/docs/{id}/threads` | open a bare note on a document or one of its arranged rules |
| `POST /api/v1/threads/{tid}/messages` | reply to any thread, a bare note included |
| `POST /api/v1/threads/{tid}/resolve` | resolve any thread, a bare note included |
| `POST /api/v1/tasks/{id}/reviewers` | wholesale replace of `task_reviewers` |
