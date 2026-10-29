
The sweeper is the server loop that expires leases past their `expires_at` and reverts their still-in-progress tasks to `ready`. It also purges old lease rows, and it keys on the lease only.

