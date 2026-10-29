
People see **Crew**, never `participants`. `GET /projects/{id}/crew` lists
each member with display name (the actor id when none is set) and role
labels; the one project lead carries a visually distinct Lead badge. An empty
roster says "No Crew yet". The page adds a member through a form (role
defaults to a plain label when none is given) and removes one per row; the
lead has no Remove button. A removal refused because the member still owns
open work re-renders with that work listed and linked, so the remover sees
what has to be reassigned or closed. The page and the JSON API
(`/api/v1/projects/{id}/participants`) share one write per change, recorded as
`crew.member_added` or `crew.member_removed`.

