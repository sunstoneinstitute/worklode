
A state-changing request (any method but GET, HEAD and OPTIONS) authorized
by the session cookie must pass the checks `beginJSONPost` applies
(WL-REQ-1338): strictly same-origin by `Sec-Fetch-Site` or `Origin`,
the header `X-Requested-With: lode-web`, and `Content-Type:
application/json`. A bearer-token request skips these checks, since it
carries no ambient credential. A refusal is JSON, never a redirect to login.

