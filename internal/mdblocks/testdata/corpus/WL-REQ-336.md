
`GET /projects/{id}` is the Overview. The canvas carries, top to bottom, the
stage stepper (§5.2), the active rally when there is one (WL-SPEC-75), the
pinned focus (§5.3), Active work (people and delegated agents, each row with
owner and delegate, evidence chip and state chip, and a New task link), and
the agent sessions running now. The decision rail carries the next decision
(§5.3), the highest-signal exception (the top secondary concern with its
evidence summary), and the automation boundary: policy and budget, which the
backbone does not store and so read "not configured", and the project's real
30-day agent spend. A card with no fact behind it is omitted. The same
projection is served as JSON at `GET /api/v1/projects/{id}/cockpit`.

