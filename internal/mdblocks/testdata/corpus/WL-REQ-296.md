
Per mapped repo, from the server: App installed (`GET /repos/{owner}/{name}/installation`, reported installed / not installed / unchecked, where only GitHub's own 404 counts as not installed; checks run concurrently under one deadline), last webhook received and event types seen, unapplied events (`*.ignored` and nil-apply rows awaiting replay), and unmapped senders (repos sending events that map to no project). No argument reports every repo. A repo that never received a webhook, or whose last delivery predates its mapping, is the signal to run reconcile.

