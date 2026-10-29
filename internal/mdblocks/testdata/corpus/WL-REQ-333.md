
An inbox icon sits on every page immediately left of the theme toggle and links
to `/inbox`. It shows a small red-toned dot when the actor has at least one
inbox item and nothing otherwise. Two constraints follow from the icon being
universal: the has-items answer is computed once per request from the session
subject and carried on the page props every page builds, and it is an existence
check that stops at the first item (`store.HasInboxItems`, one `EXISTS` over
the §12 buckets). The full ranked inbox query runs on `/inbox` only. There is
no numeric count.

