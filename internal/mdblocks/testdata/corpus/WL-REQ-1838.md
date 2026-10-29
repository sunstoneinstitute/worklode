
After the login and before any code exists, the browser shows a consent page with Allow and Deny. It names the user and the destination: "the Worklode connector in claude.ai", or "an app on this computer (localhost:PORT)".

- The pending consent lives server-side for 5 minutes, single use, keyed on a random id. The page posts the id back together with a cookie of the same value.
- `POST /oauth/consent` accepts same-origin submissions only.
- Allow mints a single-use 5-minute code bound to the PKCE challenge and redirects to the client.
- Deny redirects with `error=access_denied`.

