
An in-memory mutex-guarded map of `{actorID, state, expiresAt, used}`. 32 bytes of entropy, single use, 5-minute TTL (long enough for a human to carry a code between machines, within RFC 6749's ten-minute ceiling). The server is single-instance, so a restart drops pending codes and the user re-runs login. If the server goes multi-replica this becomes a table.

