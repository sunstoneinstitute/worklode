
`lode install` wires `pre-commit`, `commit-msg`, `post-merge`, and `post-commit` as git hooks, editor-agnostic. An existing hook is preserved as `<name>.pre-lode` and invoked through the `lode-hook` chain (WL-REQ-273). If `.pre-commit-config.yaml` exists, `pre-commit` always chains to the framework (that hook only). The installer is idempotent: it detects and re-points its own link.

