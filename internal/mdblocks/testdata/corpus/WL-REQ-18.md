
| Channel | Contents |
|---|---|
| Homebrew | `lode`, `lode-hook`, `lode-statusline`; tests their version output |
| Server container | `lode-server` (entry point), `lode-migrate`, migrations, optional ffmpeg. The Kubernetes init container runs `lode-migrate` directly. Distroless, non-root, no shell. |
| Watcher container | `lode-watch` only, published separately so Kubernetes client code is absent from the user and server distributions |

Each executable has its own build target and one aggregate target builds all seven. CI builds both images.
