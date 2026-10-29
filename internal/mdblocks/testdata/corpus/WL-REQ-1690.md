
The cockpit CSP (`script-src 'self'; style-src 'self'`, no `unsafe-inline`) does not change. reveal creates runtime stylesheets only in its PDF view and in `data-auto-animate`, and neither is used. A deck feature needing an inline stylesheet is dropped.

