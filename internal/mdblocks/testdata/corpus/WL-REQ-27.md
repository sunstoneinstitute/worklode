
An image is a cache of what `bootstrap.sh` would do. An image whose contents disagree with the script is a defect in the image. The root `Dockerfile` is the server image and is hostile to being a worker (no shell, no package manager), so workers are a separate family.

| Image | Contents | Purpose |
|---|---|---|
| `docker/base/Dockerfile` | shell, `git`, CA certificates, `lode` | The minimum that can claim a task and run a hook |
| `docker/generic/Dockerfile` | base plus the toolchains `bootstrap.sh` installs | Default when a project declares nothing |
| `<repo>/.worklode/Dockerfile` | `FROM` one of the above | A project's own additions, versioned with the project |

Worker images run `lode install --agent all --no-statusline` at build time. The status line has no reader in a sandbox.

