
An act that is session-only by design, such as approval decide, note and
exception (WL-REQ-123), gets an `/api/v1` route that accepts the session cookie
and refuses a bearer token. Its `routeGuards` entry records which one it is.

