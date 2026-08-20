# internal/cmd

Cobra commands, both server and client sides. This is the surface agent
instructions are written against.

**Changing a command, flag, `--json` shape, config key or hook name?** Follow
the checklist in `docs/agent-surfaces.md` — agent-facing markdown across this
repo and the `sunstoneinstitute/claude-plugins` marketplace hardcodes these
invocations. `go test -trimpath ./internal/cmd -run TestAgentSurfaces` names
the in-tree ones that broke; the doc covers the rest.
