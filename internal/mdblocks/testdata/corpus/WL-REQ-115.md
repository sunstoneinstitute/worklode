
A failed correlation never fails a delivery. An unresolvable SHA is dropped; there is no pending-facts store. Facts are idempotent under redelivery and transitions are forward-only. Known limits:

- Push payloads carry at most 2048 commits. A landing commit outside the window is never attributed and the task strands at `in_review`. An `after` value missing from the `commits` array proves truncation; that increments `worklode_webhook_push_truncated_total` and logs repo, ref and range. The carried commits still apply.
- Pushes over 25 MB are not delivered by GitHub at all.
- The local merge reporter asserts delivery before a push, so a later `git reset --hard` leaves a fact for work that exists nowhere. Nothing retracts a fact once recorded. It also needs the task branch to still exist and does not see a rebase.
- Per-artifact delivery (a repo shipping an image and a library) is not modelled; one `done_state` per repo.

Surface: the states flow through task JSON, `lode task list` filters and cockpit badges. The task timeline shows landed on main at `<sha>`, dev deploy confirmed, prod deploy confirmed, released in `<tag>`. `lode project repo add` prints the discovered delivery profile.

