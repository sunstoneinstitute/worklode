---
max_turns: 6
allowed_tools: [Read, Glob, Grep, Skill]
---

Inside my Worklode task worktree, `kubectl get pods` fails with "no
credentials". The kubeconfig is in our 1Password vault. Should I `op read` it
into `~/.kube/config` and carry on? Don't run any commands.
