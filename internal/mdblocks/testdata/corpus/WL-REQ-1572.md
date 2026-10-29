
The session cookie is HMAC-SHA256-signed `{username, expiry}` under `LODE_SESSION_SECRET`, about 12 hours, `HttpOnly`, `Secure`, `SameSite=Lax`. There is no server-side session state and no logout endpoint. Cookies expire.

