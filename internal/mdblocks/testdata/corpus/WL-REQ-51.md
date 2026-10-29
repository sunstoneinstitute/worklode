
The whole feature is off unless `LODE_OIDC_ISSUER` and `LODE_OIDC_CLIENT_ID` are set.

| Var | Meaning |
|---|---|
| `LODE_OIDC_ISSUER` | `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `LODE_OIDC_CLIENT_ID` | `worklode` |
| `LODE_PUBLIC_URL` | External base URL, used for the web callback redirect URI and CLI discovery |
| `LODE_SESSION_SECRET` | HMAC key for session and intent cookies. Required when OIDC is enabled. |
| `LODE_WEB_OPEN` | Serve web pages to anonymous callers when no provider is configured. Ignored when OIDC is enabled. A non-boolean value fails the boot. |

ID tokens are verified with `github.com/coreos/go-oidc/v3` (JWKS fetch and cache, signature, `iss`, `aud`, `exp`). Claims used: `preferred_username`, `name`, `email`, `groups`, `githubUsername`.

