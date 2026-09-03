# kagent integration vs. rolling our own

Research note, 2026-08-19. Question: specs 017 (task-declared secrets) and 038
(worklode in a cloud sandbox) both design bespoke machinery for provisioning
and running agent execution environments. Does
[`kagent-dev/kagent`](https://github.com/kagent-dev/kagent) — a Kubernetes-native
agent platform, CNCF Sandbox since May 2025 — already solve enough of that
ground to be worth integrating with?

**Short answer: no, and the reason is structural rather than a maturity
complaint.** kagent's unit of work is a declarative agent service answering A2A
requests; worklode's is a git worktree bound to a leased task. kagent has no
queue, no assignment, no lease, and no branch-per-task checkout, so it cannot
replace worklode's coordination layer — nor does it claim to. That leaves one
candidate integration: kagent as the executor behind 038 §5's dispatch seam.
There, it is strictly more machinery than a Kubernetes Job, and it would put
execution history in a second database, which the backbone's ownership split
forbids. On the 017 side the overlap is not merely shallow but *inverted*:
kagent's secret model requires the value at rest in a cluster-side Kubernetes
Secret, which is the exact property 017 was designed to avoid.

Recommendation: **decline, with a named trigger** — the pattern 038 §2.1
already uses for the tool-manager question. Amend 038 with a "why not" section;
leave 017 untouched. Three narrow ideas are worth borrowing directly, without
adopting kagent.

"Is kagent right?" and "is anything right?" are different questions, so §5
sweeps the wider landscape. It found one project that fits worklode
substantially better than kagent does —
[`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox)
— and it is a better fit for a specific reason: it is a pod-lifecycle
controller that stores no execution state of its own, so §3.3's decisive
objection does not apply to it. That does not reverse the recommendation, and
it is not something to adopt today. It changes what 038's new section should
name as the thing to watch.

Method: one primary-source sweep on 2026-08-19 against a shallow clone of
kagent at `30e056d1` (main, 2026-08-18) and the docs at
[kagent.dev/docs](https://kagent.dev/docs), plus a read of worklode's own
017/038 inlined specs and the shipped `internal/secrets` tree. Claims about
kagent come from its repository source, CRD chart templates, GitHub API
metadata and official docs. §5's landscape sweep is a lighter pass — public
docs, project READMEs and release feeds, no clones — and is scoped to options
that could sit behind 038 §5's seam, not to agent frameworks generally.
Conclusions marked **Synthesis** are ours.

Caveats: no substantive independent third-party evaluation of kagent was found,
so §1's limitations are project-self-reported. GitHub marks every kagent tag
`prerelease: false` including betas and rcs, so "latest stable = v0.9.12"
is inferred from tag naming alone. The 0.10 runtime model was not fully traced
— see "What we did not establish" at the end. §5 is documentation-depth only:
nothing there was run, and agent-sandbox in particular deserves a hands-on
spike before it is relied on.

---

## 1. What kagent is, in the terms this question needs

kagent declares LLM agents as Kubernetes CRDs; a Go controller reconciles them
into running workloads; a runtime built on Google's ADK executes them; a
dashboard, CLI, gRPC API and A2A endpoint are the interaction surfaces. Its
stated audience is DevOps/platform engineering, and the shipped tool set
(Kubernetes, Istio, Helm, Argo, Prometheus, Grafana, Cilium) reflects that
([what-is-kagent](https://kagent.dev/docs/kagent/introduction/what-is-kagent)).
Apache-2.0, CNCF Sandbox ([cncf/sandbox#360](https://github.com/cncf/sandbox/issues/360)),
~3.5k stars, weekly-ish patch cadence.

Four facts govern everything below.

**It is entirely pre-beta API.** Every CRD is `v1alpha*`; `ModelConfig` serves
three concurrent alpha versions; `Agent` stores `v1alpha2` while a `v1alpha3`
Go type exists unserved. The external integration surface changed shape between
releases: v0.9.12 exposed a full REST API (`/api/agents`, `/api/sessions`,
`/api/tasks`, …), and `main` replaces it with gRPC services
(`go/core/internal/grpcserver/`), leaving HTTP with only `/health`, A2A, `/mcp`
and a WebSocket proxy. Integrating today means integrating against a surface
that has already been broken once in the window we looked at.

**The runtime model is one long-lived Deployment + Service per Agent.** At
v0.9.12, `buildWorkloadObjects` in
`go/core/internal/controller/translator/agent/manifest_builder.go` emits a
Deployment, a ClusterIP Service, a ServiceAccount and a config Secret. There is
no per-invocation Job or Pod.

**The interesting part is explicitly not ready.** Agent Substrate — gVisor
per-actor isolation, snapshot-to-object-storage, rehydration onto pre-warmed
worker pools — is a separate repo whose README states it "is currently in early
development. It is not ready for production use, and the APIs are almost
guaranteed to change" ([substrate](https://github.com/kagent-dev/substrate)).
It ships disabled (`substrate.enabled: false`) and needs a separate
`ate-system` install.

**Claude Code is not a supported harness.** `AgentHarness.spec.backend` is a
CEL-enforced enum of exactly `openclaw;hermes`. A `claude` target exists in
`docker/acp-sandbox/Dockerfile` wrapping `@anthropic-ai/claude-agent-sdk`, but
its own README's Open Items say only the ACP `initialize` handshake is
verified, and "still to verify: a full `session/prompt` round-trip reaches the
model". Anthropic *models* are first-class (`ModelConfig` provider `Anthropic`,
plus Vertex and Bedrock); Claude Code as an agent is a build target, not a
feature.

---

## 2. Overlap and conflict with 017 (task-declared secrets)

### 2.1 The conflict is architectural, not a feature gap

017 §0 states the property the whole spec is built around: "**Worklode is not a
security broker.** It stores symbolic names and initiates a ceremony; it never
stores, transports, or sees a secret value." 1Password is the source of truth,
`op://` is the addressing scheme, and *the operator's own `op` session is the
decryption authority*. That last clause is what makes `op://Employee/…` —
each operator's private vault — usable at all, with no remote service account
crossing the privacy boundary.

kagent's model is the opposite by construction. Credentials are Kubernetes
Secret references from CRDs: `ModelConfig.spec.apiKeySecret` +
`apiKeySecretKey` (same-namespace only), `ModelProviderConfig.spec.secretRef`,
`caCertSecretRef`, `Agent.spec.skills.gitAuthSecretRef`, `imagePullSecrets`.
The controller resolves them and injects them into the agent pod. For a value
to reach a kagent agent, it must first exist at rest in the cluster.

**Synthesis:** adopting kagent's secret path would require every Employee-vault
credential to be copied into a cluster Secret — turning worklode's deployment
into precisely the security broker 017 declines to be, and destroying the
private-vault property. This is not a trade to weigh; it is the design premise
inverted.

### 2.2 kagent is also simply weaker here

Even setting the premise aside, kagent offers less than 017 already ships:

| 017 property | kagent equivalent |
|---|---|
| Per-task declared secret set, resolved at claim (§2, §3) | None. Credentials are static per-Agent configuration. |
| Operator consent ceremony, one `op run` authorization (§3) | None. |
| External secret manager as source of truth | **No first-party integration** — no Vault, 1Password or ESO client in the Go tree. The only mention is a `values.yaml` `extraObjects` example rendering an ESO `ExternalSecret` so ESO can populate the Secret kagent then references. |
| Materialized lifetime = worktree lifetime; purge on done/block/exit (§4) | No TTL, no revocation, no lease. Secrets live for the pod's lifetime. |
| `secrets_materialized` event, names only, in the backbone log (§3.5) | No audit event for credential grant. |

The one genuinely per-request mechanism is
`ModelConfig.spec.apiKeyPassthrough` — forwarding the Bearer token from an
incoming A2A request to the LLM provider. It covers the model credential only,
not tool, repo, or cloud credentials.

### 2.3 017 is shipped, not partial

The task brief assumed 017 was "already partially implemented". It is not:
all three phases have landed on `main` — `internal/secrets/` (catalog, envfile,
keystore, manifest, names), `internal/cmd/secretsceremony.go`, the
`lode-secrets` skill, and the catalog ConfigMap in `deploy/base/`
(477b5a6 → ce26318 → c053799 → ede95a7 → df40e80). The spec's status is still
`draft`, but the code is in the trunk and exercised by the claim path.

**Synthesis:** the migration question for 017 is therefore not "finish it
differently" but "replace working code with something weaker that breaks its
central privacy property". **No amendment to 017 is warranted.**

### 2.4 Two ideas worth stealing anyway

kagent's skill runtime scrubs the environment before running an agent-issued
shell command. `_sanitize_env()` in
`python/packages/kagent-skills/src/kagent/skills/shell.py` strips a denylist —
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_*`, `GOOGLE_APPLICATION_CREDENTIALS`,
… — plus a regex over secret-looking names, so LLM credentials are not visible
to code the agent runs.

`lode secret exec` today injects exactly the task's materialized names into
the child (017 §4), which is the right *positive* rule. It says nothing about
what else is already in the operator's environment and gets inherited. A
denylist pass on inherited variables is a small, contained hardening.

Second: kagent hands its sandbox a **default-deny egress policy** —
`Agent.spec.sandbox.network.allowedDomains`, where the API doc comment states
"when unset or when allowedDomains is empty, outbound access is denied by
default". 038 has no egress story at all. The posture is worth copying even
though the mechanism is not.

Both are follow-ups, not spec changes.

---

## 3. Overlap and conflict with 038 (worklode in a cloud sandbox)

This is where kagent has something real, and where the disappointment is sharpest.

### 3.1 What genuinely matches

038 §0's motivating scenario is "a coding agent started from a phone, running
in a provider-managed cloud sandbox". kagent's `AgentHarness` CRD provisions "a
long-running remote coding-agent sandbox", with `spec.channels` for Slack and
Telegram — start a coding agent from your phone, near-literally. Agent
Substrate's README lists as a compatibility target "**Claude Code & CodeX**:
support for high-density, stateful coding environments that preserve terminal
and filesystem state across sessions".

Substrate's primitives map onto problems 038 has not solved: gVisor kernel
isolation per actor, suspend/resume, `CreateCheckpoint`/`ForkAgentInstance`,
and pre-warmed worker pools that would answer 038 §8's unresolved cold-start
and image-build-cost questions.

**Synthesis:** if Substrate were stable and its Claude Code target shipped,
this would be a genuine build-vs-buy decision on 038 §3 (worker images) and
§4 (the sandbox session). It is neither. The overlap is with the part of
kagent that is explicitly pre-production, disabled by default, and whose APIs
are "almost guaranteed to change" — while 038's own §3–4 are unbuilt, so the
comparison is between two things that do not exist yet.

### 3.2 Where it does not match at all

**No task-scoped checkout.** kagent's git support is
`Agent.spec.skills.gitRefs` — a `skills-init` init container running
`git clone --depth 1 --branch <ref>` to populate `/skills` at pod start
(`go/core/internal/skillsinit/git.go`). It is read-only knowledge loading.
There is no branch-per-task working copy, no commit, no push, no PR. Worklode's
entire execution model — `lode work next` creates a worktree, commits are the
heartbeat, `worktree.Layout.TaskID` resolves the bound task from the
filesystem, `lode secret exec`'s guard refuses to run outside one — hangs off
exactly the concept kagent does not have.

**No work-item lifecycle.** kagent's `task` table is
`id, created_at, updated_at, deleted_at, data TEXT, session_id` — an opaque A2A
JSON blob keyed to a session. States (`submitted`/`working`/`completed`) come
from the A2A protocol. There is no queue, no assignment, no lease, no claim,
no dispatcher matching pending work to workers. Tasks are created by whoever
invokes the agent and run by that agent. The closest thing to a lifecycle gate
is human-in-the-loop tool approval.

### 3.3 The decisive objection: a second owner of execution facts

kagent persists full execution history in **its own PostgreSQL** — `session`,
`event`, `task`, `agent_instance`, `agent_instance_task_event`,
`push_notification`, `feedback`, plus framework checkpoint tables — with
queryable prompt/tool-call/token history and a prompt-audit feature.

Worklode already owns that. `internal/transcript` and `store/pricing` price an
agent session from the agent's own transcript against effective-dated
`model_prices` rows; the append-only event log is the provenance record; 004
makes the backbone the owner of execution facts. CLAUDE.md's rule is flat:
*no fact has two owners.*

**Synthesis:** routing execution through kagent means either (a) kagent's DB
becomes a second, authoritative record of what ran and what it cost, which the
split forbids, or (b) worklode's cost and provenance pipeline is rewired to
read from kagent's alpha-versioned schema. Neither is a cost worth paying for a
container launcher.

### 3.4 What worklode would actually need behind the seam

038 §5 already names the four dispatch seams — image selection, provisioning
(`bootstrap.sh`), token acquisition, entry point (`lode work next --json`) — and
states the goal: dispatch should be "the human path parameterised, not a second
implementation".

What that seam needs from an executor is: *start a container from tag T, inject
`LODE_SERVER`/`LODE_TOKEN`, run `lode work next --json`, let it exit.* That is a
Kubernetes Job. Worklode already has a Kubernetes client and a pod informer
(`internal/watch`), and 039 already puts both instances in clusters we run.

Going through kagent to obtain that costs: a controller, ten-plus CRDs, a
second Postgres, an alpha API churning across three concurrent versions, a
REST→gRPC break already observed, and the dual-ownership problem in §3.3 — in
exchange for a scheduling primitive Kubernetes provides directly.

**Synthesis: the overlap at the seam that matters is too shallow to justify the
dependency, and the overlap that would be deep (Substrate) is not shippable.**

---

## 4. Recommendation and spec-change sketch

**Do not integrate with kagent.** Revisit on a named trigger, not on a schedule.

### 4.1 Spec 017 — no change

The conflict is with 017's founding premise (§2.1) and kagent is weaker on
every axis 017 specifies (§2.2), against an implementation that has already
shipped (§2.3). Nothing to amend, supersede, or defer.

### 4.2 Spec 038 — one new section, one line in §7

Add **§5.1 "Why not an external agent runtime, yet"**, placed under §5 because
the dispatch seam is the only place kagent could have landed. Model it on
§2.1's existing "Why not a tool manager, yet" — declined on evidence, with a
trigger, not on taste. It should state:

- kagent has no task-scoped checkout and no lease/claim lifecycle, so it
  substitutes for none of §1's unchanged concerns;
- what the seam needs is a Kubernetes Job, which the cluster already provides
  and `internal/watch` already has a client for;
- kagent would introduce a second owner of execution facts, which 004's split
  forbids;
- the part that would genuinely help — Agent Substrate's gVisor isolation,
  snapshotting and pre-warmed pools — is pre-production by its own statement,
  and its Claude Code harness is an unwired build target.

**The trigger to revisit is dispatch actually being built, not a better
controller appearing** — phrased as a trigger, matching §2.1's "the trigger to
revisit is a second runtime, not a second opinion". This note's §5.4 gives the
wording, and explains why an earlier draft tied the trigger to kagent's
Substrate roadmap instead: that was too narrow, because a better-shaped
project already exists outside it. The new 038 section should name
`kubernetes-sigs/agent-sandbox` as the incumbent candidate to evaluate when
that moment comes, with a plain Kubernetes `Job` as the baseline it has to
beat.

Then add one line to **§7 Out of scope**: "An external agent runtime. Declined
with a named trigger (§5.1), not forgotten."

No section of 038 is superseded. §3 (worker images), §4 (the sandbox session)
and §5 (seams) stand as written — §5.1 explains why the seam stays filled by
worklode's own code.

### 4.3 Follow-ups to file (not spec changes)

1. `[P3]` **Scrub inherited credentials in `lode secret exec`.** 017 §4
   specifies which names are injected; it does not say what inherited
   environment is stripped. Add a denylist pass over inherited variables
   (`ANTHROPIC_API_KEY`, `AWS_*`, `GOOGLE_APPLICATION_CREDENTIALS`, …) so a
   task's child sees its declared set rather than the operator's shell.
   Prior art: kagent's `_sanitize_env()`.
2. `[P4]` **038 has no egress posture.** Worker images grant whatever the pod
   network grants. Decide whether a default-deny outbound allowlist belongs in
   §3 before images are built — it is far cheaper to specify now than to
   retrofit.
3. `[P4]` **Watch `kubernetes-sigs/agent-sandbox`, not Substrate.** Re-read it
   when 038 §3–4 implementation starts, against the trigger in this note's
   §5.4. Warm pools and hibernate/resume of a stateful session are the two
   things worklode would not build itself; this note's §5.1 records what to
   re-check (API churn since v0.5.5, and whether our clusters carry a gVisor
   or Kata `RuntimeClass`).
4. `[P4]` **Mint task-scoped tokens the ARC way.** When 038 §4.3's
   "transitional" operator token is replaced, copy Actions Runner Controller's
   just-in-time pattern (this note's §5.2): the dispatcher mints a per-worker
   credential bound to the claimed task, the worker never persists it, and it
   expires with the lease.

---

## 5. The rest of the landscape

kagent was the candidate the question named. Declining it only answers whether
*that* project fits; it says nothing about whether some other project should
sit behind 038 §5's provisioning seam. This section sweeps that ground.

The filter is §3.3's ownership test, applied first rather than last: any
candidate that keeps its own record of what happened during a session is
disqualified before its features are worth reading, because the backbone owns
execution facts. That test removes most of the field, and it is what makes the
one survivor interesting.

### 5.1 `kubernetes-sigs/agent-sandbox` — the closer fit

A Kubernetes SIG Apps subproject, announced by GKE engineers in November 2025,
that does one thing: manage the lifecycle of an isolated, stateful, singleton
pod ([project](https://github.com/kubernetes-sigs/agent-sandbox),
[docs](https://agent-sandbox.sigs.k8s.io/docs/),
[announcement](https://opensource.googleblog.com/2025/11/unleashing-autonomous-ai-agents-why-kubernetes-needs-a-new-standard-for-agent-execution.html)).
Four CRDs on `agents.x-k8s.io/v1beta1`: `Sandbox` (a pod with a stable
hostname and optional persistent storage), `SandboxTemplate`, `SandboxClaim`,
and `SandboxWarmPool` (pre-provisioned pods for sub-second allocation). The
controller handles creation, scheduled deletion after a TTL, and
hibernate/resume — a suspended sandbox stops costing compute and wakes on
network activity. It is explicitly a *sandbox orchestrator*: isolation is
delegated to gVisor or Kata Containers via `RuntimeClass` rather than
implemented. Go and Python SDKs ship with it (`sigs.k8s.io/agent-sandbox`).

Four things make it a different proposition from kagent:

- **It stores nothing.** There is no session table, no event log, no task
  record — the CRs are the state and the persistent volume is the workspace.
  §3.3's objection, which was decisive against kagent, does not arise. It is a
  mechanism the backbone could drive without becoming a second owner of
  anything.
- **It is scoped to the seam.** kagent wanted to own the agent; this owns the
  pod. Worklode keeps the queue, the lease, the branch, the brief, the
  transcript pricing and the event log, and hands out one job: run this
  container, this long, this isolated.
- **`SandboxWarmPool` answers a question 038 has open.** Cold start is the
  cost that makes backbone-initiated dispatch feel bad, and pre-warmed pools
  are the standard answer to it — the one genuinely useful thing kagent's
  Substrate promised, here in a shipping beta instead of a pre-production one.
- **Hibernate/resume matches 038 §0 literally.** §0's motivating case is an
  agent started from a phone with an ephemeral filesystem and no operator
  present. A sandbox that suspends when idle, survives with its volume intact,
  and resumes on the next connection is that scenario's shape, not an
  approximation of it.

**Where it still does not fit, and this matters as much as the above:**

- **Two different needs are being conflated.** 038 §0's phone-started session
  is a stateful singleton; 038 §5's dispatched worker is a batch job that
  claims, works, pushes and exits. agent-sandbox is built for the first. For
  the second it is strictly more machinery than a Kubernetes `Job`, which is
  still the right primitive there. Adopting it would serve §0, not §5 — and §5
  is the seam this research was asked about.
- **Its headline feature is worth less to worklode than to its target user.**
  gVisor and Kata exist because the general case runs untrusted
  model-generated code from strangers. Worklode runs our own agent against our
  own repositories with our own credentials. The residual threat — prompt
  injection via repository content or fetched pages — is real but does not
  justify a kernel boundary on its own, and 038 never claimed it did.
- **It fills half of one of the four seams.** Provisioning is still
  `bootstrap.sh`; image selection, token acquisition and entry point are
  untouched. It does not build images, so 038 §8's Q1 (where worker images
  live) and Q3 (who builds `.worklode/Dockerfile`) survive intact, and it does
  no secret handling at all, so 017 is unaffected either way.
- **It is moving fast.** v0.5.5 landed 2026-08-13, six days before this note,
  on a roughly weekly cadence. `v1alpha1`→`v1beta1` is already behind it with
  a published migration guide, and there are breaking changes *within* the
  v0.5 line: `spec.replicas` replaced by `spec.operatingMode`, `SandboxClaim`
  switched from `templateRef` to `warmpoolRef`, `volumeClaimTemplates` made
  immutable after creation, plus an upgrade advisory for a warm-start race in
  v0.5.0–v0.5.1. Beta backed by a SIG is not the same thing as an API worth
  writing into a spec today.
- **The isolation is not available to us, and would be a separate project.**
  Neither `hzdev` nor the admin cluster (039) runs a gVisor or Kata
  `RuntimeClass` today — operator-confirmed, 2026-08-19. So adopting
  agent-sandbox as things stand buys pod lifecycle management and nothing
  else; the isolation it is chiefly known for arrives only if someone installs
  and then operates a second container runtime. That is a real cost to weigh
  separately, not a switch the controller flips. It also means the feature is
  not a blocker for evaluating the rest: a proof of concept runs fine on
  `runc`, and warm pools and hibernate/resume — the parts worklode would
  actually use — do not depend on it.

**Synthesis: right shape, wrong seam, too early — but it is the project to
watch, and kagent is not.** If worklode ever provisions long-lived sandboxes
rather than dispatching short-lived workers, this is the first thing to
evaluate, against a plain `Job` as the baseline it has to beat. Note what that
comparison is really about: with no `RuntimeClass` in either cluster, the case
for agent-sandbox over a `Job` rests entirely on warm-pool cold starts and
hibernate/resume, not on isolation.

### 5.2 Actions Runner Controller — prior art, not a dependency

GitHub's [ARC](https://docs.github.com/en/actions/concepts/runners/actions-runner-controller)
is worth reading precisely because it is not adoptable: it is GitHub-specific,
and its queue is the Actions service. But its architecture is 038 §5's four
seams already built and proven at scale. A listener pod long-polls the service
for `JobAvailable`/`JobAssigned` and scales an `EphemeralRunnerSet`; each
runner pod starts, receives a **just-in-time token**, registers, runs exactly
one job, and exits without reuse.

Two things follow. First, it is independent confirmation that the Job-shaped
design in §3.4 is the right one — the closest real system to what 038 §5
describes reached the same shape without a sandbox controller. Second, the JIT
token is the answer to a problem 038 §4.3 already names and defers: it calls
the long-lived operator token "transitional and known to be so" and wants "a
short-lived token bound to the claimed task and expiring with its lease". ARC
shows that pattern working, with the listener minting per-runner credentials
the runner never persists. Worth copying when §4.3's replacement is designed.

### 5.3 Ruled out, with reasons

| Option | What it is | Why not |
|---|---|---|
| [E2B](https://e2b.dev), [Daytona](https://daytona.io), [Modal](https://modal.com), Vercel Sandbox | Managed sandbox-as-a-service. Firecracker microVMs (E2B), gVisor (Modal), containers (Daytona); ~90–150 ms cold starts; billed per vCPU-hour | Moves execution off infrastructure 039 already runs, adds a vendor and a per-hour bill for a capacity problem we do not have, and creates a new trust boundary for 017's materialised secret set. Answers neither image build nor token minting. Daytona's persistent-by-default workspace is the closest match to 038 §0 should a vendor ever be wanted. |
| [Claude Managed Agents](https://modal.com/blog/introducing-claude-managed-agents-with-modal-sandboxes) incl. self-hosted sandboxes | Anthropic runs the agent loop and session state; a provider (Modal, Cloudflare, Daytona, Vercel) executes tool calls | §3.3's objection one level up — Anthropic would hold session state that the backbone owns and already prices from transcripts. It also targets *building custom agents*, not driving Claude Code across a repository, so it is not the same job. |
| [Argo Workflows](https://argoproj.github.io/workflows/), [Tekton](https://tekton.dev) | Kubernetes DAG and CI engines | Worklode's DAG is the task graph in Postgres, with dependencies, blockers and ranking already modelled there. Adopting either adds a second scheduler, and Argo a second store of run status. A `Job` is the primitive; a workflow engine is a layer worklode already has. |
| [OpenHands](https://github.com/All-Hands-AI/OpenHands) and peers | Self-hostable coding agent; issue → branch → PR | Competes with Claude Code, not with 038. Whether to run a different agent is a real question and a separate one; it does not bear on provisioning. |

### 5.4 What the sweep changes

The recommendation on kagent stands unchanged — nothing found here makes it a
better fit. What changes is §4.2's trigger, which was drawn too narrowly.

Naming *Agent Substrate reaching a stable API and shipping a Claude Code
harness* as the condition to revisit ties 038 to one vendor's roadmap, and
§5.1 shows a better-shaped project already exists outside it. The trigger
should name the **circumstance**, matching §2.1's "the trigger to revisit is a
second runtime, not a second opinion":

> **The trigger to revisit is dispatch actually being built, not a better
> controller appearing.** When 032 §8's agent pools reach implementation,
> evaluate `kubernetes-sigs/agent-sandbox` first — against a plain Kubernetes
> `Job` as the baseline it must beat — and adopt it only if long-lived,
> resumable sandboxes are wanted rather than one-shot workers.

Follow-up 3 in §4.3 changes accordingly: watch agent-sandbox, not Substrate.

---

## 6. What we did not establish

- **No independent technical evaluation of kagent exists** that we could find.
  Searches returned vendor blogs and listicles. §1's limitations are
  project-self-reported, read from its own code and docs.
- **The 0.10 runtime model was not fully traced.** v0.9.12 demonstrably builds
  a Deployment + Service per Agent (read at the tag). On `main` the translator
  targets `SandboxAgent` → Substrate ActorTemplate; whether a plain `Agent` CR
  still gets a Deployment in the 0.10 line was not confirmed. This does not
  change the recommendation — both models are per-agent-service, not
  per-task-checkout — but do not quote the 0.10 description.
- **Per-agent RBAC blast radius was not measured.** The translator builds a
  per-agent ServiceAccount; `helm/kagent/templates/rbac/` was not read, so how
  much cluster authority a compromised agent inherits is unknown. Would matter
  if the recommendation were reversed.
- **`srt` isolation strength was not evaluated.** We confirmed which
  sandbox-runtime kagent installs (Anthropic's, bubblewrap-based) and what
  policy it receives, not how strong that boundary is. Relevant to follow-up 2
  if we borrow the posture.
- **No documented outbound event stream.** A `push_notification` table implies
  A2A push callbacks work; there is no docs page and the handler was not read.
- **§5 is documentation-depth only.** Nothing in the landscape sweep was
  cloned, built or run. agent-sandbox's release-note details in §5.1 came from
  a summarising fetch of the releases page, not the raw notes; the version
  numbers and dates were verified independently against the release feed, the
  per-release API changes were not. Treat §5.1's breaking-change list as
  indicative of churn rather than as a migration checklist.
- ~~Whether our clusters can deliver agent-sandbox's isolation is unknown.~~
  **Resolved 2026-08-19:** neither cluster runs a gVisor or Kata
  `RuntimeClass`. What is still unknown is the cost and appetite for
  installing and operating one — see §5.1, where this now sits as a finding
  rather than a gap.
- **agent-sandbox has no named adopters.** The announcement cites ADK and
  LangChain as integration points, not as users, and no production reference
  was found. Its scale claims — tens of thousands of parallel sandboxes — are
  project-stated, like kagent's.
