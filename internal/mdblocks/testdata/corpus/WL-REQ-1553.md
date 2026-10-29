
Server-owned loops stay in `lode-server`: lease sweeping, document lifecycle, skill sync, graph projection. They share server configuration, storage, metrics and shutdown. `lode task reconcile` and `lode blob gc` are operator commands in `lode` that call authenticated server APIs. Interactive streaming commands such as `lode event tail --follow` stay in the CLI.

