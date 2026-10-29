
```
lode secret exec [--] <command> [args…]
```

Resolves the bound task from the worktree, reads that task's items from the keystore, builds the child environment, and `syscall.Exec`s. Nothing is written, logged or echoed. The child keeps the parent environment minus every credential-shaped name, matched case-insensitively (`internal/secrets.CredentialShaped`):

- kept first, whatever else matches: shell plumbing (`PATH`, `HOME`, `SHELL`, `USER`, `PWD`, `TMPDIR`, `TERM`, `LANG`, `TZ` and similar), `SSH_AUTH_SOCK`, `SSH_AGENT_PID`, the four `GIT_AUTHOR_*`/`GIT_COMMITTER_*` identity variables, and the `LC_` and `XDG_` namespaces;
- dropped: the `AWS_`, `AZURE_`, `GCP_`, `CLOUDSDK_`, `ANTHROPIC_`, `OPENAI_`, `VAULT_` and `OP_` namespaces; `KUBECONFIG`, `NETRC`, `DOCKER_CONFIG`, `PGPASSFILE`, `GNUPGHOME`; any name containing `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `PWD`, `PASSPHRASE`, `CRED`, `AUTH` or `APIKEY`; any name ending in `_KEY` or containing `_KEY_`.

The rule is a deny-list by name shape, so a credential in a variable named for none of these shapes still passes. Materialized names are injected after the scrub, and any ambient assignment of an exported name is stripped first. The injected set is exactly the task's materialized entries. For a plain entry, exec injects `<env>=<value>`.

