
The cockpit is session-gated. OIDC login mints a web session cookie that is
`HttpOnly`, `Secure` and `SameSite=Lax`. Every cockpit route has an entry in
`internal/api/router.go`'s `routeGuards`, and `NewServer` refuses to boot on a
route the table does not name. Reads need `permWebRead` and form writes
`permWebWrite`. A write that is also a JSON API capability carries that
capability's permission: restoring a deleted task or publishing a task
`permTaskWrite`, restoring a document `permDocWrite`, the Progress page's
writes `permDocWrite` or `permTaskWrite` (WL-SPEC-85). Deciding an approval
(`permApprovalDecide`), writing an impact note (`permApprovalNote`) and
advancing the Morning Brief boundary also require a live web session, so no
bearer token reaches them.

