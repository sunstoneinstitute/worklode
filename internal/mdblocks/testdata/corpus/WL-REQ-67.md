
Tasks declare the secrets they need by symbolic name, the same way they pin skills (WL-SPEC-81). The system resolves names against an org-wide catalog and materializes values through a ceremony the operator takes part in, before execution goes unattended. This removes mid-task stalls on interactive prompts, makes detached executors possible, and gives a session exactly the credentials its task declared, auditable in the event log.

Worklode is not a security broker. It stores names and templates and starts a ceremony. 1Password is the source of truth for values, `op://` URLs are the addressing scheme, and the operator's own `op` session is the decryption authority. `op://Employee/…` therefore resolves against each operator's private vault with no service account crossing that boundary.

