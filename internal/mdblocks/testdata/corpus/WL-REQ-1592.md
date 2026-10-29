
One process holds a subscriber's stream: the loop takes a dedicated pool connection and `pg_try_advisory_lock(hashtext('wl:subscriber:' || name))` for its lifetime. A second replica fails the lock, idles and retries. Release calls `pg_advisory_unlock` and then destroys the session rather than returning it to the pool; any lock-path error is treated as "not held". The loop polls (default 1s); there is no `LISTEN`/`NOTIFY`.

