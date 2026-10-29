
A plan becomes `executed` when the last task it minted closes, and stays `accepted` if it minted none. A plan with a `covers` edge to a withdrawn rule becomes `stale`. Re-planning a stale plan is claimed with `lode work next --replan`, which claims an open re-planning design task or mints one; re-accepting the edited plan clears `stale`.

**Stale plans.** An unexecuted plan with a `covers` edge to a rule whose section is amended in place becomes `stale` and a re-planning task is minted. Its published but unclaimed tasks are re-derived with the plan or flagged at claim time.
