
The executor behind the dispatch seams (WL-REQ-31) is a Kubernetes `Job`. `kagent` is declined because it owns no task-scoped checkout or lease lifecycle and keeps its own execution history. `agent-sandbox` is declined because its warm-pool advantage needs a claim-then-push shape that changes two seams. Re-evaluate it, against a plain `Job`, when dispatch is designed to drive a running pod.

