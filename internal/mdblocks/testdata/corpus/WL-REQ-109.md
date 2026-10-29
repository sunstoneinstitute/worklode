
Five rules, a pure `Evaluate(event) → []Action` in `internal/watcher`, executed by `internal/api/docwatch.go`. Each rule name is also the `rule` label of `worklode_watcher_actions_total`.

| Rule | Event | Action | Guard |
|---|---|---|---|
| `review-on-submit` | `wl:DocumentSubmitted` | mint `kind = 'review'`, state `ready`, `about_doc` set | no open review task about the document |
| `approval-on-submit` | `wl:DocumentSubmitted` | materialize the document's `awaiting` approval row (WL-REQ-124) | no open approval bound to the document |
| `plan-on-accept` | `wl:DocumentAccepted` where the document is a spec | mint `kind = 'design'`, state `ready`: decide how to decompose this spec into plans, and write them | no open design task about the document |
| `review-on-patch` | `doc.patched` classified substantive | mint `kind = 'review'` to re-review the patched sections | no open review task about the document |
| `groom-on-stale` | `doc.stale` | mint `kind = 'design'`: "Groom:" for a spec, "Re-plan:" for a plan | no open design task about the document |

Accepting a plan mints nothing here, because the accept transaction already publishes the plan's tasks. No rule accepts a document.

Minted tasks take the document's project and carry `prov:wasInformedBy` to the causing event. Idempotency has two layers: redelivery is refused by the action's own deterministic `external_id`; a legitimate second acceptance while the planning task is open is absorbed and noted on it (`task.updated`), and once closed a further acceptance mints a fresh task. Submission is an event and changes no document column: the open review task is what "under review" means. A subscriber action must not emit an event its own subscriber consumes. The subscriber loop starts only when the server is given a background context.

