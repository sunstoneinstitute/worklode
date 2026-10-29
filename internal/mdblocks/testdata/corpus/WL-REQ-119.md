
Each investigation is one project. Its key prefixes every identifier and its lifecycle is the investigation's. Research projects own no git repo. The research monorepo maps once to a standing umbrella project, which opens the webhook gate; inside it each investigation directory carries `.worklode/config.toml` with `current_project`, nearest config wins. Correlation matches on task id alone, so a PR on `COW-7-slug` attaches to `COW-7` whatever project the repo maps to.

