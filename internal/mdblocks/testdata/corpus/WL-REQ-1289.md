
Timestamps MUST be stored in UTC. That holds for every Postgres column, event payload and API field that carries a point in time. A timestamp that crosses the API is RFC 3339 in UTC.

