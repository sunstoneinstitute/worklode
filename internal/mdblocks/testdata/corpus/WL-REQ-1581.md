
For a templated entry, `lode secret exec` (WL-REQ-72) fetches the credential items, renders the manifest's template, writes the result to `.worklode/secrets/<NAME>` in the worktree (directory 0700, file 0600, temp-file-plus-rename), records the absolute path in the manifest, and injects `<env>=<absolute path>`. The path is stable and re-rendered on every exec, so the file heals after deletion or a worktree move. `.worklode/secrets/` and `.worklode/secrets.env` are in the repo's local git exclude.

The rendered file lives until purge. Exec ends in `syscall.Exec`, so per-invocation cleanup would need fork/wait, which changes the process contract and still leaks on a SIGKILLed parent, and a long-running child may read `KUBECONFIG` late. The residual exposure is a plaintext 0600 file on the operator's single-user machine for the same window the credentials sit in the keystore.

