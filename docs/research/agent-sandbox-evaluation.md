# agent-sandbox as the executor behind 038 §5

Research note, 2026-08-19. Question: WL-115 declined `kagent-dev/kagent` and, while
sweeping alternatives, found
[`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox)
— a SIG Apps subproject managing the lifecycle of an isolated, stateful, singleton
pod. That assessment was documentation-depth only. Does agent-sandbox survive a
primary-source pass, and should worklode adopt it behind 038 §5's dispatch seam?

**Short answer: not yet, but for a narrower reason than WL-115 gave, and the
trigger is different.** The two objections that would have killed it do not hold:
it stores no more execution state than a `Job` does (§2), and it beats a cold
`Job` on allocation latency by two to three orders of magnitude (§3) — measured,
on `runc`, with isolation off the table exactly as the brief requires. What blocks
adoption is a shape mismatch that is sharper than "over-specified for a batch
job": **a warm pool can only serve a claim that carries no per-task
configuration.** A `SandboxClaim` that sets `spec.env` — the obvious way to pass
`LODE_TOKEN` and a task id — silently cold-starts, and cold-starting is where the
entire measured advantage lives (§4). Using warm pools therefore requires worklode
to differentiate the pod *after* it is running, over the network, which is a
different dispatch design from 038 §5's four seams rather than a drop-in behind
them.

Recommendation: **decline for now, with a trigger that names a design decision
rather than a maturity milestone** (§7). This supersedes the placeholder trigger
WL-115 proposed for the unwritten 038 §5.1. Spec 017 stays untouched, and §6.4
explains why its design is in fact the *better* fit under warm pools than 038
§4.3's environment-injected token is.

Method: one primary-source pass on 2026-08-19. Source read at a full clone of
`kubernetes-sigs/agent-sandbox` at `ac864a6` (main, 2026-08-18), with API and
release facts checked against tag `v0.5.5` (`3ea199b`, 2026-08-13). Release notes
read as raw GitHub release bodies via `gh release view`, not as summaries. The
proof of concept ran controller image
`registry.k8s.io/agent-sandbox/agent-sandbox-controller:v0.5.5`, installed from
the published `sandbox-with-extensions.yaml` asset, on a single-node `kind`
v0.32.0 cluster (Kubernetes v1.36.1, containerd 2.3.1, plain `runc`, no
`RuntimeClass`). Every number and behaviour in §2–§4 was measured by three Go
programs driving the cluster through the project's own generated typed clientsets
and watching for readiness with millisecond resolution. Worklode's own 017/038
inlined specs and `go.mod` were read for the integration sections. Conclusions
marked **Synthesis** are ours.

Caveats: the cluster is one node on one laptop, so the absolute latencies are a
floor and the scheduling numbers say nothing about queueing behaviour under real
cluster load or node scale-up — which is where a cold `Job` gets much worse and
the gap in §3 gets much wider, not narrower. No production-shaped worker image was
built; §3's large-image run uses `python:3.13` (413 MB on the node) as a stand-in.
Nothing was run at scale, so the project's density claims are untested here. The
sandbox router, the in-pod `sandboxd` runtime and the Python SDK were read but not
exercised. Isolation is out of scope by the task brief and was not evaluated at
all: no gVisor or Kata `RuntimeClass` exists in either of our clusters, and the
case below is made on lifecycle management alone.

---

## 1. What it actually is, verified at source

Four CRDs, two API groups, one controller binary.

| Kind | Group | Purpose |
|---|---|---|
| `Sandbox` | `agents.x-k8s.io/v1beta1` | One pod with a stable name, optional PVCs and an optional headless Service |
| `SandboxTemplate` | `extensions.agents.x-k8s.io/v1beta1` | A reusable blueprint plus a NetworkPolicy and injection policies |
| `SandboxWarmPool` | `extensions.agents.x-k8s.io/v1beta1` | N pre-created `Sandbox`es from a template |
| `SandboxClaim` | `extensions.agents.x-k8s.io/v1beta1` | Checks one sandbox out of a pool |

The `extensions` group is a separate Go package tree
([`extensions/api/v1beta1/`](https://github.com/kubernetes-sigs/agent-sandbox/tree/v0.5.5/extensions/api/v1beta1))
and a separate controller enabled by a `--extensions` flag on the same
Deployment. Three of the four CRDs — including everything that makes warm pools
work — live behind that flag. **Synthesis:** the part of agent-sandbox worklode
would actually depend on is the part the project itself has marked as an
extension, not the core.

`SandboxSpec` carries a `podTemplate`, `volumeClaimTemplates` (immutable after
creation), a `service` toggle, an `operatingMode` of `Running` or `Suspended`, and
a `lifecycle` with `shutdownTime` and a `shutdownPolicy` of `Delete` or `Retain`
([`api/v1beta1/sandbox_types.go`](https://github.com/kubernetes-sigs/agent-sandbox/blob/v0.5.5/api/v1beta1/sandbox_types.go)).
That is the whole surface. There is no `backoffLimit`, no `completions`, no
`activeDeadlineSeconds` and no `ttlSecondsAfterFinished` on `Sandbox`; retry is
not a concept the controller has.

Observed behaviour, all from the PoC:

- **Deleting the pod under a running Sandbox recreates it, under the same name.**
  A sandbox whose pod was force-deleted was back and `Ready=True` within twenty
  seconds, restart count zero, same pod name. This is what "stable singleton"
  buys over a bare pod, and it is different in kind from a `Job`, which replaces a
  failed pod with a differently-named one.
- **A container that exits non-zero is not retried.** With
  `restartPolicy: Never`, a container exiting 7 left the pod `Failed` and the
  Sandbox at `Ready=False reason=PodFailed`, `Finished=True reason=PodFailed`. The
  controller records the outcome and stops.
- **Suspend really terminates the pod and keeps the volume.** After patching
  `operatingMode: Suspended`, zero pods remained in the namespace and the PVC was
  retained. Suspension took 31.2 s, but that is the pod's 30 s default
  `terminationGracePeriodSeconds` and not controller latency.
- **Resume restores the workspace.** Resume to `Ready` took 469 ms, against 4.573 s
  for the original create (which included PVC provisioning), and a marker file
  written into the PVC before suspension was intact afterwards, with both boot
  records present.

## 2. The ownership test, re-verified against source

This is the claim WL-115 built from a README and flagged as unreliable, and it is
the claim that decided WL-115 against kagent. It holds, with one correction to how
it is stated.

`SandboxStatus` has six fields: `serviceFQDN`, `service`, `conditions`,
`selector`, `podIPs` and `nodeName`
([source](https://github.com/kubernetes-sigs/agent-sandbox/blob/v0.5.5/api/v1beta1/sandbox_types.go)).
Five are live pointers at a running pod. The sixth is a condition list, and it
carries `Finished=True` with reason `PodSucceeded` or `PodFailed` once the pod
reaches a terminal phase.

On expiry under `shutdownPolicy: Retain` the controller explicitly "drop[s]
live-resource status while retaining terminal conditions"
([`controllers/sandbox_controller.go`](https://github.com/kubernetes-sigs/agent-sandbox/blob/v0.5.5/controllers/sandbox_controller.go)).
The PoC confirms exactly that: an expired retained Sandbox held `Finished=True
reason=PodSucceeded` with its transition timestamp, `Ready=False
reason=SandboxExpired`, and one annotation (`agents.x-k8s.io/pod-name`), with
`podIPs`, `nodeName` and `selector` cleared.

**Synthesis: "it stores nothing" is the wrong phrasing; "it stores no more than a
`Job` does" is the right one, and that is what the test requires.** A retained
Sandbox records that a pod succeeded or failed and when — the same class of fact
as a `Job`'s `status.succeeded` and `completionTime`, and one worklode's event log
already owns independently. There is no session record, no transcript, no task
identity and no history beyond the last transition. Setting `shutdownPolicy:
Delete` removes even that. WL-115 §3.3's objection does not arise, and this is now
verified rather than inferred.

## 3. Cold start against the baseline it has to beat

Measured, not estimated. Each figure is wall time from immediately before the
create call to the first watch event reporting readiness — pod `Ready=True` for a
`Job`, the `Ready` condition for a `Sandbox` or `SandboxClaim`.

With `busybox:1.36` (2.22 MB) already on the node, n=10 each:

| Path | min | median | max |
|---|---|---|---|
| cold `Job` → pod Ready | 454 ms | **465 ms** | 500 ms |
| cold `Sandbox` → Ready | 446 ms | **470 ms** | 489 ms |
| warm `SandboxClaim` → Ready | 11 ms | **12 ms** | 13 ms |

With `python:3.13` (413 MB on the node) as a stand-in for a real worker image:

| Path | result |
|---|---|
| cold `Job`, image not yet on the node | **9.46 s** (single run) |
| cold `Job`, image cached, n=5 | median **478 ms** |
| warm `SandboxClaim`, n=5 | median **12 ms** |

Three things follow. A cold `Sandbox` is indistinguishable from a cold `Job` —
the controller adds no measurable overhead, and it adds no benefit either. A warm
`SandboxClaim` is roughly 40× faster than either, and the multiple grows with
image size because a pooled pod has already pulled and started the image. And the
`Job` baseline's real weakness is the image pull: 9.46 s here on one warm-cached
laptop node, which is the optimistic end of what a multi-gigabyte worker image
costs on a cold node in a real cluster.

**Synthesis: 038 §8's open cold-start problem is genuinely answered by warm pools,
and by nothing else in the `Job` toolbox.** But read the 12 ms precisely: it is
the time to *own a running pod*, not the time until an agent is working. The repo
clone, `bootstrap.sh`, and `lode work next` all still lie ahead, and on a real task
those dominate everything measured here. Warm pools remove the image pull. They do
not remove the setup, unless a pool is pre-seeded per project — which multiplies
pools by projects and is not something the controller helps with.

## 4. The shape question, and where it actually breaks

WL-115 concluded that agent-sandbox serves 038 §0's stateful phone-started session
and is over-specified for 038 §5's claim-work-push-exit worker. The first half is
right and §1's suspend/resume evidence strengthens it: a suspended sandbox costs no
compute, keeps its volume, and resumes in under half a second with the workspace
intact. That is 038 §0's scenario literally, not by analogy.

The second half is right for the wrong reason. "More machinery than a `Job`" would
be a weak objection given §3's numbers. The real objection is sharper and is a
measured fact:

**A `SandboxClaim` that sets `spec.env` cannot be served from the warm pool.** The
field's own documentation says so — "adding this field means the Sandbox will
always be cold-started from the template of the warmpool", because environment
variables are baked into a pod before creation and cannot be injected into a
running one
([`extensions/api/v1beta1/sandboxclaim_types.go`](https://github.com/kubernetes-sigs/agent-sandbox/blob/v0.5.5/extensions/api/v1beta1/sandboxclaim_types.go)).
The PoC confirms it against a template with `envVarsInjectionPolicy: Allowed` and
a pool of three ready replicas: a claim with no env was served warm in 14 ms and
its Sandbox carried `agents.x-k8s.io/launch-type: warm`; an otherwise identical
claim setting one variable took 185 ms and carried `launch-type: cold`. The same
applies to `volumeClaimTemplates` on a claim.

038 §5's seam table substitutes exactly two per-task values into the environment:
a minted task-scoped token, and a task id for the entry point. Passing either as
`spec.env` forfeits the warm pool and leaves a `Sandbox` that is a cold `Job` with
more CRDs.

The escape is the one the project's own Go SDK is built around. `Client.CreateSandbox`
takes a warm-pool name and returns a handle whose methods are `Run`, `Write`,
`Read` and `List`
([`clients/go/sandbox/`](https://github.com/kubernetes-sigs/agent-sandbox/tree/v0.5.5/clients/go/sandbox)),
served by an in-pod HTTP runtime on port 8888. The intended pattern is: keep the
pool generic, claim an anonymous pod, then push the task in over the network.

**Synthesis: that pattern works for worklode, and it is a different design from
038 §5.** Under it the dispatcher does not hand a fully-parameterised pod to the
scheduler and walk away; it claims a generic pod and then drives it. The token
stops being an environment variable and becomes a file written into a running
container. The entry point stops being a container command and becomes a remote
exec. Two of §5's four seams — token acquisition and entry point — change
substance, not just source. That is a spec amendment, not a substitution behind an
existing seam, and it is the reason to decline today rather than a reason the
project is unsuitable.

## 5. API stability, from the raw release notes

WL-115 read a summarising fetch and flagged its breaking-change list as
indicative. Read raw, the picture is milder than that list implies and the
correction matters.

| Release | Date | Breaking change |
|---|---|---|
| v0.5.0 | 2026-06-24 | `v1alpha1`→`v1beta1`; `spec.replicas` → `spec.operatingMode`; `SandboxClaim.templateRef` → `warmPoolRef`; sandbox-router must live in `agent-sandbox-system` |
| v0.5.1 | 2026-07-09 | none |
| v0.5.2 | 2026-07-16 | Python SDK `use_pod_ip` removed; `manifest.yaml` renamed to `sandbox.yaml` |
| v0.5.3 | 2026-07-23 | `Sandbox.spec.volumeClaimTemplates` made explicitly immutable |
| v0.5.4 | 2026-07-30 | `Suspended` condition now always present; reason `PodNotTerminated` deprecated for `PodTerminating` |
| v0.5.5 | 2026-08-13 | Python SDK minimum version raised to 3.11 |

**Correction to WL-115 §5.1:** the three changes it cited as "breaking changes
*within* the v0.5 line" — `operatingMode`, `warmpoolRef`, immutable
`volumeClaimTemplates` — are two v0.5.0 changes that were part of the `v1beta1`
graduation itself, plus one v0.5.3 tightening of a constraint that was already
implicit. Since graduation, one Go-visible API break has shipped (v0.5.4's
condition semantics) and the rest have been Python SDK and packaging. Tag dates
confirm the roughly weekly cadence at source, and 34 commits landed on main in the
six days after v0.5.5.

The more useful maturity signal is elsewhere: **warm pools have been the buggy
part.** v0.5.0 and v0.5.1 shipped with a published upgrade advisory about a
status-wiping race that silently cold-restarts warm claims; v0.5.2 fixed two
distinct warm-adoption regressions; v0.5.4 fixed warm-pool over-creation under
informer-cache lag; v0.5.5 added refill-rate shaping. Four consecutive releases
touching correctness in exactly the feature worklode would depend on.

Governance is real rather than nominal — 848 commits and 144 distinct authors in
the last twelve months, about 47% of commits from `google.com`, and the OWNERS
approvers include a Red Hat maintainer alongside three Google ones — but this is
still a project whose headline feature is being stabilised in public.

**Synthesis: the CRD schema is stable enough to write into a spec; the warm-pool
behaviour is not stable enough to depend on yet.** Which is inconvenient, because
the warm pool is the only reason to adopt it.

## 6. Integration cost and shape

### 6.1 The Go dependency is genuinely cheap

Worklode already requires `k8s.io/api`, `k8s.io/apimachinery` and
`k8s.io/client-go` at v0.36.3, which is the exact version agent-sandbox is built
against. Importing its generated typed clientsets adds one module
(`sigs.k8s.io/agent-sandbox`) and pulls in a single controller-runtime package,
the interface-only `pkg/conversion`. No version conflict, no controller-runtime
manager, no scheme surgery. Both the core and extensions groups ship generated
clientsets, listers and informers. Worklode's existing pod informer in
`internal/watch` is the same client-go machinery.

### 6.2 The image gains a second daemon

Using `Run`/`Write` means the worker image must ship the project's in-pod runtime
listening on port 8888, beside `lode` and the agent. 038 §3.1's image family gains
a component whose lifecycle worklode does not control and whose absence is not
detectable until a claim is driven.

### 6.3 Failure mid-task is worklode's problem either way

A pod deleted under a running Sandbox comes back (§1), which is better than a bare
pod and irrelevant to a task that has already lost its process. A container that
exits non-zero is not retried at all. So the sandbox neither resumes the agent nor
retries it: the lease expires and the task returns to the frontier, which is
already 038 §1's stated behaviour. **Synthesis: nothing here needs the sandbox's
help, and hibernate/resume is not a substitute for it either.** Resume restores a
volume, not a dead agent process. Weighed against simply letting the lease expire
and re-claiming, hibernate wins only when the workspace is expensive to rebuild
and the same agent will return to it — which is 038 §0's operator-driven session,
not a dispatched worker.

### 6.4 017 stays untouched, and is the better fit

Confirmed: agent-sandbox does no secret handling of any kind. Nothing in the four
CRDs references Kubernetes `Secret`s, and 017's design is unaffected.

There is more to say than "unaffected". 017 materialises values into the
executor's OS keystore at claim time and injects them per-child via `lode secrets
exec` (017 §3, §4). That is an in-pod, post-start mechanism — precisely the shape
§4 shows warm pools require, and precisely the shape 038 §4.3's environment-injected
`LODE_TOKEN` is not. **Synthesis: if warm pools are ever adopted, 017 needs no
change and 038 §4.3's token plumbing is the thing that has to move.**

## 7. Recommendation

**Decline for now.** Not on maturity, not on isolation, and not on "more machinery
than a `Job`" — on the fact that the only version of agent-sandbox worth adopting
is the warm-pool version, and the warm-pool version requires a dispatch design
worklode has not decided on.

The trigger WL-115 proposed for the unwritten 038 §5.1 named the circumstance
"dispatch actually being built". That is close, but it points at the wrong
decision. Dispatch could be built exactly as §5 describes — a parameterised pod
handed to the scheduler — and agent-sandbox would still be pointless, because §4
shows that design cannot use a warm pool. The trigger should name the design
decision that makes the project relevant:

> **The trigger to revisit is deciding that dispatch drives a running pod rather
> than parameterising a new one.** 038 §5's four seams describe a worker
> configured at creation, and a warm pool cannot serve that: a `SandboxClaim`
> carrying `spec.env` cold-starts, forfeiting the entire measured advantage.
> Evaluate `kubernetes-sigs/agent-sandbox` when — and only when — 032 §8's agent
> pools are designed to claim a generic pod and push the task into it. At that
> point the comparison is warm-claim allocation (measured at 12 ms) against a
> cold `Job` (465 ms warm-cached, 9.5 s with a cold image pull), and the
> precondition is that warm-pool adoption has gone one full release line without
> a correctness fix.

**Sections of 038 that would need amending if adopted**, stated so the eventual
§5.1 has them to hand:

- **§5**, the seam table: "Token acquisition" and "Entry point" both change
  substance under warm pools — a file written into a running container and a
  remote exec, not an environment variable and a container command.
- **§4.3**, identity: the replacement short-lived token cannot arrive as
  `LODE_TOKEN` in the pod's environment if the pod is claimed from a pool.
- **§3.1**, the image family: worker images gain the in-pod sandbox runtime.
- **§8**, open questions: Q1 (where worker images live) and Q3 (who builds
  `.worklode/Dockerfile`) are untouched — agent-sandbox builds no images. Its
  contribution is to close the cold-start question §8 does not currently ask
  outright.
- **§7**, out of scope: unchanged. Holding cloud credentials and driving a
  provider API stays 032's work.

**017: no change.** Verified in §6.4, and its claim-time materialisation is more
compatible with warm pools than 038's current token plumbing is.

Both the §5.1 text and this trigger should land in one edit once the
recommendation is accepted, as WL-115's own proposed §5.1 is still unwritten.

---

## What we did not establish

- **Nothing was run at scale.** One node, one laptop, at most ten concurrent
  objects. The project's density claims, its behaviour under informer-cache lag —
  the thing v0.5.4 shipped mitigations for — and warm-pool refill under sustained
  churn are all untested here.
- **No real worker image was built or driven.** §3's large-image figure uses
  `python:3.13` as a stand-in. The end-to-end latency that matters — claim to an
  agent actually working a task, including clone and `bootstrap.sh` — was not
  measured for either path, and it is plausibly large enough to shrink §3's gap
  in relative terms.
- **The in-pod runtime and the SDK's remote-exec path were read, not exercised.**
  §4's escape hatch is therefore established as the intended design from the SDK's
  own surface, not demonstrated working. Whether `Write`/`Run` is a sound way to
  deliver a task-scoped token — and what the pod-to-pod authentication story is —
  is unexamined and would be the first thing to test if the trigger fires.
- **The sandbox router was not deployed.** Ingress to a claimed sandbox, and
  whether a phone-started session can reach one, is unevaluated.
- **The controller's RBAC blast radius was read but not judged.** Its ClusterRole
  grants cluster-wide `create`/`delete`/`patch` on pods, PVCs and services in
  every namespace. That is unremarkable for a workload controller and would still
  need a decision before it ran in 039's admin cluster.
- **Isolation was not evaluated, deliberately.** Neither cluster has a gVisor or
  Kata `RuntimeClass`, the PoC ran on `runc`, and the case above is made without
  it, per the task brief. What a `RuntimeClass` would cost to install and operate
  remains a separate, unasked question.
- **Still no named production adopters.** As WL-115 found; nothing in the source
  or the release notes changes that.
