
A worker's outbound network is deny by default. The baseline allowlist:

- the Worklode API ingress (`LODE_SERVER`)
- the model provider endpoint the harness talks to
- the git hosts of the project's mapped repos
- language-ecosystem registries (`proxy.golang.org`, `sum.golang.org`, `registry.npmjs.org`, `pypi.org`, `files.pythonhosted.org`), because a checkout's dependencies move faster than its image
- the worker-image registry

A project needing more lists hosts one per line in `.worklode/egress.txt`, reviewed with the code that needs it. The mechanism (NetworkPolicy, egress proxy, provider allowlist) belongs to the deployment holding the cloud credentials. Exfiltration through an authorised push to the git host remains possible. Credential scoping in WL-SPEC-74 is the primary control.

