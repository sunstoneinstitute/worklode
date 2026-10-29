
Environment normalization applies to GitHub environment names only: `dev`, `test`, `development`, `staging` are dev; `prod`, `production` are prod; everything else is ignored. `LODE_CLUSTER_ENV_MAP` (cluster to stage, for Flux) is operator config validated at startup to contain only `dev` and `prod`.

