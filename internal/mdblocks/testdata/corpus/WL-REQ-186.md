
```
lode doc list                       live documents: ref, status, title
lode doc list --kind spec|adr|plan
lode doc list --status <status>|all
lode doc list --owner <actor>
lode doc list --needs-planning      accepted specs with a requirement section no accepted plan covers
lode doc list --needs-execution     accepted plans whose minted tasks include an open one
lode doc list --bare-superseded     withdrawn rules no rule supersedes
```

Filters compose. `--json` emits the same rows as objects. Without `--status`, withdrawn, superseded and executed documents are hidden; `--status all` shows them. `--unresolved`, `--older-than`, `--deleted` and `--has-notes` are defined in WL-SPEC-77.

Each derived selector implies a status and a kind, and a contradicting `--status` or `--kind` is an error, because an empty result would read as "nothing to do". `--needs-planning`, `--needs-execution`, `--bare-superseded` and `--unresolved` are mutually exclusive.
