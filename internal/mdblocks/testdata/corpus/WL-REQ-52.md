
Kept as GitOps in the admin cluster's `keycloak-config/rbac.yaml`. Only group memberships are edited in the console.

| Item | Value |
|---|---|
| Client `worklode` | Public client, standard flow, PKCE S256, `fullScopeAllowed: false`. One client for every environment. |
| Redirect URIs | `http://localhost:8080/auth/callback`, `https://<host>/auth/callback` |
| Protocol mappers | `client-roles-as-groups` (client roles arrive in the `groups` claim); user attribute `githubUsername` as an ID-token claim of the same name |
| Client roles | `user` (required to log in), `admin` (maps to `actors.admin`) |
| Groups | `/apps/worklode` carries `worklode:user`; `/apps/worklode/admins` carries `worklode:admin` |

