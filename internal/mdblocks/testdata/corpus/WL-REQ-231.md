
Per-object hardening is `Content-Disposition` alone: no CSP and no `X-Content-Type-Options` are set on a served blob, because S3 exposes no `response-*` override for them and user metadata becomes `x-amz-meta-*`, which no browser acts on. Uploads send no user metadata. The redirect lands in a different origin from the app, so a hostile payload executes there.

