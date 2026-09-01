---
status: draft
issued: 2026-08-21
kind: adr
requires:
- 017-task-secrets.md
- 047-loader-sensitive-secret-names.md
amends:
  "#sec-2":
  - 017-task-secrets.md#sec-4
  - 017-task-secrets.md#sec-10
---
# ADR 050 — The `lode secrets exec` child inherits a scrubbed environment

## 0. Decision {#sec-0}

`lode secrets exec` scrubs the environment it inherits before handing it to the
child. Assignments whose **name** is credential-shaped — `ANTHROPIC_API_KEY`,
`AWS_*`, anything containing `TOKEN`, `SECRET`, `PASSWORD`, `AUTH`, and the
rest of §3 — are dropped. Everything else, including the shell plumbing the
child needs to run at all (`PATH`, `HOME`, `TMPDIR`, the locale variables), is
inherited unchanged.

**A deny-list, not an allow-list.** An allow-list gives the stronger
guarantee. It was considered for v1 and rejected: the variables a task's
child genuinely needs are open-ended — a compiler's cache directory, a proxy
setting, a tool's config path — so an allow-list breaks working tasks in ways
that show up as confusing failures deep in a build. A deny-list, by
contrast, only fails by missing a credential it did not recognise. The trade
is a weaker guarantee, for a change that can ship without surveying every
tool an agent runs; §4 lists what it therefore does not cover.

The rule lives in `internal/secrets.ChildEnv`, alongside the strip-and-inject
pass that was already there.

## 1. The problem {#sec-1}

017 §0 puts least privilege among the three reasons the feature exists: "a
session should hold exactly the credentials its task declared — not whatever
the operator's environment happens to contain". 017 §4 then specifies the
*positive* half of that — the injected set is exactly the task's materialized
names — and says nothing about the rest of the environment.

So the negative half was never implemented. `lode secrets exec` passed
`os.Environ()` through, stripping only the names it was about to inject (so
the task's value wins over an ambient one with the same name). A child
therefore saw every credential the operator's shell happened to export: the
`ANTHROPIC_API_KEY` that starts the agent, `AWS_*` from a `direnv`, a
`GITHUB_TOKEN` from a dotfile. The careful per-task scoping gained nothing,
because the ambient set arrived anyway.

Prior art: kagent's `_sanitize_env()`
(`python/packages/kagent-skills/src/kagent/skills/shell.py`) strips a
comparable deny-list before running an agent-issued shell command. The finding
is recorded in `docs/research/kagent-integration.md` §2.4.

## 2. What the child inherits {#sec-2}

The child's environment is, in order:

1. the parent environment, minus every assignment whose name the task
   materialized (unchanged — this is what makes the injected value
   authoritative, since execve keeps duplicates and `getenv` returns the
   first);
2. minus every assignment whose name is **credential-shaped** by §3;
3. plus the task's materialized assignments.

Injection happens after the scrub, so a materialized name that is itself
credential-shaped — `GITHUB_TOKEN`, most of the catalog — is unaffected by the
deny rules. The deny-list judges *inherited* names only.

Step 2 matches case-insensitively: an inherited name is whatever the operator's
shell exported. Step 1 does not, because a materialized name is upper-case by
the 017 §1 grammar.

**017 §10's acceptance criterion 4 reads, as amended:** in the claimed
worktree, `lode secrets exec -- env` shows the materialized names and the shell
plumbing of §3's keep set, and shows no credential-shaped inherited variable —
in particular no `ANTHROPIC_API_KEY` exported in the parent shell. The same
command in a plain checkout fails the worktree guard, and no secret value
appears in any file, log, or event row.

## 3. The list {#sec-3}

**Keep, whatever else matches.** `PATH`, `HOME`, `SHELL`, `USER`, `LOGNAME`,
`PWD`, `OLDPWD`, `TMPDIR`, `TERM`, `TERMINFO`, `LANG`, `LANGUAGE`, `TZ`,
`COLUMNS`, `LINES`, `SSH_AUTH_SOCK`, `SSH_AGENT_PID`, `GIT_AUTHOR_NAME`,
`GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL`, and the `LC_`
and `XDG_` namespaces. The keep set is checked first, so a deny pattern can
never grow far enough to break this plumbing by accident. The git identity
pair is here precisely because `AUTHOR` contains `AUTH`: a child that kept the
committer variables while losing the author ones would commit under a
mismatched identity, or fail outright. `SSH_AUTH_SOCK` is a deliberate member
too. It is an ambient credential channel, but the Linux keystore (017 §3) is
encrypted to a key held in ssh-agent, and `git push` over ssh needs that key —
so stripping `SSH_AUTH_SOCK` would break `lode` itself inside the child.

**Deny by namespace** — these namespaces exist to carry an identity, and even
their non-credential members pick which ambient credential gets used
(`AWS_PROFILE` picks a key pair out of `~/.aws`; `ANTHROPIC_BASE_URL` picks
which endpoint a key is presented to):

`AWS_`, `AZURE_`, `GCP_`, `CLOUDSDK_`, `ANTHROPIC_`, `OPENAI_`, `VAULT_`, `OP_`.

**Deny by name** — files and directories of ambient credentials whose names
carry none of the tokens below: `KUBECONFIG`, `NETRC`, `DOCKER_CONFIG`,
`PGPASSFILE`, `GNUPGHOME`.

**Deny by shape** — a name containing any of `TOKEN`, `SECRET`, `PASSWORD`,
`PASSWD`, `PWD`, `PASSPHRASE`, `CRED`, `AUTH`, `APIKEY`, or ending in `_KEY`
(or containing `_KEY_`). This matches the token anywhere in the name, not
just at the end, because the shape can appear in any position:
`GITHUB_TOKEN`, `TOKEN_FOR_REGISTRY`, `GOOGLE_APPLICATION_CREDENTIALS`. The
tokens are deliberately short forms: `CRED` also matches `*_CREDS` and
`*_CREDS_FILE` as well as `CREDENTIALS`, and `PWD` matches MySQL's
`MYSQL_PWD` — safe only because `PWD` and `OLDPWD` are in the keep set. `KEY`
alone is not a pattern, because that would also match `KEYCLOAK_URL` and
`KEYBOARD_LAYOUT`; the `_KEY` tail instead catches `GIT_SIGNING_KEY` and
`AWS_SECRET_ACCESS_KEY` while leaving `KEYCLOAK_URL` alone.

**Where the line is.** The list covers names that *look like* credentials.
It deliberately does not try to cover credentials that do not: a token in
`MY_THING`, a password in `DB_DSN`, a private key path in `IDENTITY_FILE`.
Those need the allow-list this ADR declined, or a per-task declaration.

This is a different boundary from ADR 047's, and the two do not overlap. 047
denies loader-sensitive names as *secret names* — what a task may materialize.
This ADR denies credential-shaped names as *inherited* variables — what the
child gets for free. A name can be legal in one and denied in the other:
`GITHUB_TOKEN` is a fine secret name and a stripped inherited variable, `PATH`
the reverse.

## 4. What this does not cover {#sec-4}

**Credentials in unremarkable names.** As §3's closing paragraph notes, the
scrub reads names, not values, so a credential in a name that doesn't look
like a credential is still inherited. This is the deny-list's basic
weakness, and the reason to revisit the allow-list once there is evidence
about what tasks actually need.

**`SSH_AUTH_SOCK`.** Kept by design, so the child can use every key the
operator's agent holds. Removing it needs the keystore's Linux backend to stop
depending on the agent first.

**Credential files.** `~/.aws/credentials`, `~/.config/gh/hosts.yml` and files
like them are reachable through `HOME`, which the child keeps. The scrub only
scopes the environment; it is not a filesystem sandbox. 038's sandbox is
where that boundary gets drawn.

**Everything except `lode secrets exec`.** An agent that runs a command
directly rather than through `lode secrets exec` inherits the operator's
environment as before. The scrub is a property of the exec path, not of the
session.

## 5. Consequences {#sec-5}

**A task that quietly relied on an ambient credential now fails.** That is the
point: the failure is the intended shape. A credentialed command failing
inside `lode secrets exec` is a block signal naming the missing secret
(017 §6), not something to work around. The fix is a catalog entry and a
`--secrets` declaration — never a re-export.

**False positives are silent.** A variable that matches by shape but carries
configuration — say `SOMETHING_AUTHORITY` — disappears without a message,
because printing what was stripped would put the operator's credentials in a
log. If a task needs such a variable, that is a reason to narrow the
pattern, with a test alongside it.

**The list will drift.** New providers ship new namespaces. Like 047's, this
list is a snapshot, and it degrades slowly rather than opening a hole: the
positive rule of 017 §4 is unchanged and is still the guarantee about what the
child *has*.
