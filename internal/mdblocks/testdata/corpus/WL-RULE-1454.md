
The keystore is the local store holding a task's materialized secrets: one macOS keychain item per secret, or on Linux a file encrypted to an ephemeral key held in ssh-agent. Items are purged when the worktree lease ends.

