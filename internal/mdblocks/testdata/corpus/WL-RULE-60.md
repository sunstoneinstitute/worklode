
- `redirect_uri` is loopback only, which blocks exfiltration and open redirect.
- `state` is round-tripped and checked by the CLI (CSRF).
- Codes are single-use, short-lived, high-entropy, and bound to actor plus `state`.
- The intent cookie is signed and short-lived.
- Manual mode gives up the guarantee that the code lands in the process that requested it, the same residual risk every device-code flow carries. It is bounded by single use and the 5-minute life, and used only when loopback cannot run.

