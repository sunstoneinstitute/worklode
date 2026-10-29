
Every page carries a CSP: `default-src 'self'`, `script-src 'self'`, `style-src 'self'`, `img-src` and `media-src` plus the blob origin, `object-src`, `base-uri`, `frame-ancestors` `'none'`, `form-action 'self'`. The task page CSP lists the object-storage endpoint in `img-src` and `media-src`. Form POSTs pass a same-origin check (`Sec-Fetch-Site`/`Origin`) on top of the `SameSite=Lax` cookie (WL-SPEC-82).

