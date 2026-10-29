
Isolation is **READ COMMITTED**. Claim correctness rests on the `FOR UPDATE` row lock plus the `leases_active` unique backstop (WL-REQ-95), so SERIALIZABLE is not needed. The sweep loop and migrations are safe under multiple replicas because every effect is idempotent through the event log; the subscriber loops elect one consumer with an advisory lock (WL-SPEC-75#sec-9.4a).

