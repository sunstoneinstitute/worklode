
Every member sees, in this order:

1. reviews assigned to them;
2. open reviews with no `required_actor` in projects they lead (`is_lead`);
3. reviews they own;
4. work assigned to them;
5. work they own;
6. other active work in projects they are a member of.

The order descends from "blocked on this exact person" to "happening near this
person"; it is the one-primary-decision rule of WL-REQ-339 applied across a
portfolio. Review buckets order oldest-open first by approval creation time,
ties by id. Work buckets order by the deterministic exception ranking det-v1
(`docs/research/cockpit-exception-ranking.md` §6): one entry per root cause,
scored by best held priority, fan-out and age, with unranked items after them
by priority, then id. The ranker is fed the all-projects read so blocked-by
chains resolve across project boundaries, and concerns are filtered to
membership afterwards; filtering first would truncate chains and name the
wrong root. An empty bucket is not shown.

