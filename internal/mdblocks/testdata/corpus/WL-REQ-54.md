
When OIDC is enabled, every cockpit page requires a valid session cookie (WL-SPEC-74#sec-5a) and otherwise redirects (302) to `/auth/login`. When OIDC is unconfigured, the web routes answer 503 naming the missing configuration, unless `LODE_WEB_OPEN` is set.

| Route | Behaviour |
|---|---|
| `GET /auth/login` | 302 to the Keycloak authorize URL. Auth-code plus PKCE. `state` and the PKCE verifier travel in a short-lived signed cookie. |
| `GET /auth/callback` | Redeems the code at Keycloak, verifies the ID token, provisions the actor (WL-REQ-53), then calls `finishLogin`. |

`finishLogin(w, r, actorID)` is the shared tail of every login and has three branches, chosen by the CLI-intent cookie (WL-REQ-56): absent sets the session cookie and redirects to the requested page; loopback intent mints a one-time code and redirects to the CLI; manual intent renders the code on a page.

