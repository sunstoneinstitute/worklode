---
status: draft
issued: 2026-08-20
kind: adr
requires:
- 017-task-secrets.md
amends:
  "#sec-2":
  - 017-task-secrets.md#sec-1
  - 017-task-secrets.md#sec-8
  - 042-secret-templates.md#sec-2
---
# ADR 043 — Where the secrets catalog lives

## 0. Decision {#sec-0}

The org secrets catalog is not a file in any git repository. It is a
**1Password item** named `worklode-secrets-catalog` — one per environment's
vault — and each environment's overlay projects its vault's item into the
cluster as a Kubernetes **Secret** of the same name via an ExternalSecret. `catalog.toml` — and each spec 042 template — is a
field label on that item; the server mounts the projected Secret and reads
`catalog.toml` from it exactly as before.

Adding or editing a catalog entry is a 1Password edit. It requires no pull
request against any repository, public or private.

## 1. The problem {#sec-1}

Spec 017 §1 places the catalog in "a ConfigMap in the worklode service
deployment", changed "through the deployment repo's normal PR flow",
"deliberately not in a potentially-public repo: vault and item names are
themselves mildly sensitive."

The implementation shipped `deploy/base/secrets-catalog.yaml` in *this*
repository, which is public. Nothing leaked — the ConfigMap's `catalog.toml`
holds only commented-out examples — but it was the designated home for real
`op://` entries, so the first real entry would have published the org's vault
and item topology. 017's own sensitivity argument had been inverted by the
placement.

The obvious repair — move the ConfigMap to a private deployment repo — keeps
the whole PR flow for data that is a list of pointers into 1Password. That
flow costs a repository, a reviewer, and a Flux reconciliation for every
catalog entry, and it still stores vault topology in a git history that
outlives the entry.

## 2. Where the catalog lives {#sec-2}

The catalog is a 1Password item, `worklode-secrets-catalog`, in the same vault
each environment's `worklode-secrets` item already lives in. Vaults are
per-cluster (`<cluster>-app-worklode`, the convention behind the
ClusterSecretStore names below), so that is one item **per environment**, not
one org item; what that costs is a consequence in §4. Each field label
on that item becomes a key of the projected Secret: `catalog.toml` holds the
TOML, and a spec 042 template (`kubeconfig-hzdev.yaml`) is a sibling field
label on the same item.

Each overlay carries `externalsecret-worklode-secrets-catalog.yaml`, matching
the `externalsecret-worklode-secrets.yaml` pattern beside it: the same
`ClusterSecretStore` (`onepassword-hzdev-worklode` /
`onepassword-hzprod-worklode`), `target.name: worklode-secrets-catalog`,
`creationPolicy: Owner`.

**`dataFrom.extract` on the item, not an explicit `data:` list.** This is the
load-bearing choice. `extract` projects every field label on the item as a key
of the Secret, so a new catalog entry, or a new template, is a 1Password edit
and nothing else. An explicit `data:` list would name each key in the overlay
and thereby reintroduce, per template, the exact pull request this ADR removes.
Built-in 1Password item fields arrive as extra keys in the mount; the server
reads only the path `LODE_SECRETS_CATALOG_PATH` names and ignores the rest.

**A Secret, not a templated ConfigMap.** ESO's 1Password provider produces a
Secret, and that is what the sibling `worklode-secrets` ExternalSecret already
produces; rendering a ConfigMap instead would need `template` machinery for no
gain. It is also the object kind 017's sensitivity argument asks for: a list of
`op://` references should not sit in a world-readable in-cluster object.

Nothing about the server changes. `deploy/base/deployment.yaml` mounts the
Secret at `/etc/worklode/secrets-catalog` with `optional: true`, and
`LODE_SECRETS_CATALOG_PATH` still points at `catalog.toml` under that
directory. The server reads a file; which object kind supplied it is invisible
to it.

## 3. What ships in this repo {#sec-3}

`deploy/base/secrets-catalog.yaml` is deleted. Base ships no catalog object at
all — the Secret exists only where an overlay's ExternalSecret creates it.

`deploy/secrets-catalog.example.toml` documents the shape of the item's
`catalog.toml` field. It is fully commented, no kustomization references it,
and it is never deployed: it exists so an admin editing the 1Password item can
see what the file is supposed to look like without reading the parser.

The base deployment's volume is `optional: true`, so base alone, a local
`docker compose` stack, and any cluster without the item all start normally
with no catalog. `GET /api/v1/secrets/catalog` then returns 404 "secrets
catalog not configured" — the same answer it already gives for an unset
`LODE_SECRETS_CATALOG_PATH`.

## 4. Consequences {#sec-4}

**The catalog is no longer reviewable by pull request.** A wrong `op://`
reference, a `baseline = true` nobody agreed to, or a removed entry now lands
without a second pair of eyes. The audit trail becomes 1Password's own item
history — who changed which field, when — and the control becomes the
population that can edit the item, which is the vault's write ACL rather than
a repository's `CODEOWNERS`. That is a real reduction in review and is accepted
deliberately: the catalog holds no values, and its content is a list of
pointers whose blast radius is bounded by 1Password's own access control on the
items pointed at.

**The authoritative map is now one copy per environment.** 017 §1 defines the
catalog as one org-wide map, but vaults are per-cluster, so each environment's
vault carries its own `worklode-secrets-catalog` item — hzdev's and hzprod's
today, a third when the admin-cluster prod deployment lands. The copies do not
race — a worklode server only ever serves its own cluster's item — but an
org-wide entry is one edit per vault, nothing mechanical detects drift between
them, and a name that resolves against one environment's server warns as
missing against another's. The operational rule: an org-wide entry is mirrored
by hand into every vault whose cluster runs a worklode server; an entry
meaningful in only one environment may deliberately exist only there. If hand
mirroring proves error-prone, that is churn evidence for Q17.4 below.

**Edits propagate on ESO's clock, not git's.** A 1Password edit reaches the
server only after the ExternalSecret's `refreshInterval` (1h) and the
kubelet's Secret-volume sync — up to about an hour, silently, where the PR
flow this replaces at least made propagation observable through Flux. The
manual override is ESO's `force-sync` annotation on the ExternalSecret.

**ESO reconciliation is all-or-nothing.** A malformed field on the item — or a
missing one, if a `data:` list is ever added — fails the whole ExternalSecret,
so the entire `worklode-secrets-catalog` Secret goes stale or is never created.
The overlays already carry this caveat for `LODE_EMBEDDING_API_KEY` on
`worklode-secrets`, where a missing 1Password property would yield
`CreateContainerConfigError` on a fresh install. Here the failure is softer,
because the mount is `optional: true`: the pod still starts, and the catalog
endpoint 404s or serves the last successfully projected content. The
consequence is that a broken catalog degrades claims (017 §6, "catalog
unavailable") rather than announcing itself, and the place to look is the
ExternalSecret's status, not the pod's.

**Local and development installs have no catalog.** Without the 1Password item
there is no Secret, no mount, and no file — which is a 404, not a crash. Spec
017 §6 already covers what that means downstream: the claim succeeds, the brief
warns, and credentialed steps block rather than improvise.

**017 §9's Q17.4 stays open.** That question — promote the catalog to a
backbone table plus an admin CLI if churn grows — is about whether a file is
the right long-term shape. This ADR settles only where the file lives. Moving
it out of git makes editing cheap enough that the churn argument weakens, but
it does not answer the question, and nothing here should be read as closing it.
