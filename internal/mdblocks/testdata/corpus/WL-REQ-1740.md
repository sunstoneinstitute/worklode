
`GET /api/v1/inbox?project=<id>` filters issues by joining `project_repos` on the issue's repo. Empty or absent keeps org-wide behaviour. An unknown project id yields an empty list. `GET /api/v1/board?project=` and `GET /api/v1/tasks?project=` do one-or-all.


