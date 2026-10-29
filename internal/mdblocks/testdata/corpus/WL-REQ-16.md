
Each `cmd/<name>` entry point imports only its application package and shared leaf packages. `internal/cmd` is the `lode` CLI surface and imports none of `internal/api`, `internal/store`, `internal/watch`, `internal/hookrun`, `internal/statusline`.

`lode-hook` and `lode-statusline` parse arguments with the standard library. Their transitive imports must not contain Cobra, the CLI command tree, Goldmark, server or store packages, Prometheus, or Kubernetes packages.

`internal/disttest/deps_test.go` fails the build when one of those imports creeps into `lode-hook` or `lode-statusline`, and when `cmd/lode` reaches `internal/api`, `internal/store`, `internal/watch`, `internal/hookrun`, `internal/statusline` or a Kubernetes package.

