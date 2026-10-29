
`deploy/overlays/admin/` differs from hzdev in:

- Ingress host `worklode.sunstoneinstitute.ai` in both TLS hosts and the rule host.
- `LODE_PUBLIC_URL` set to that host. Login and webhook callbacks render from it.
- `secretStoreRef` names the admin cluster's `ClusterSecretStore`. 1Password item and property names match hzdev.
- `LODE_INSTANCE_ENV: prod`.
- OIDC client `worklode-prod` against the same issuer.

`LODE_CLUSTER_ENV_MAP` is identical in every overlay. The namespace carries `sunstone.institute/ghcr-pull` in every overlay.

