
A stored user token is bounded by the intersection of the GitHub App's permissions, its installation scope (WL-REQ-63) and the user's own access. Because the App holds Contents: write, a compromised server holding both the database and `LODE_TOKEN_ENC_KEY` can push as any linked user to any installed repository that user can write. Three things bound that: the installation excludes the provisioning and admin-cluster repos, `main` accepts changes only through the merge queue, and no-bypass rulesets on the provisioning and admin repos guard independently.

