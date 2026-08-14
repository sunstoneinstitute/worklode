---
status: draft
covers:
  - docs/specs/004-execution-backbone.md#sec-5.2
  - docs/specs/004-execution-backbone.md#sec-5.3
---
# Artifact correlation hardening implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the flux-revision → artifact → task chain actually connect, by
storing a real commit SHA on release artifacts, minting `docker_image`
artifacts from `registry_package` webhooks, and correlating OCI-digest Flux
revisions against those artifacts instead of dropping them.

**Architecture:** Four independent correlation defects, each fixed where the
fact is first recorded, with no new tables. The `release` handler stops
writing a branch name into `artifacts.source_sha` and writes the commit the
frontier logic already resolved. A new `registry_package` handler mints
`docker_image` artifacts carrying the OCI digest. The Flux handler learns that
a revision is either a git ref or an OCI digest, and correlates a digest via
the artifact instead of failing a `main_commits` lookup. Finally, a
`target_commitish` that is neither a known main commit nor the default branch
is resolved to a SHA through the GitHub App — outside the delivery
transaction, because the apply callback runs inside `RecordEvent`'s `sql.Tx`
and must never make an outbound HTTP call.

**Tech Stack:** Go 1.26, Postgres via `database/sql` + pgx, `net/http` mux,
`internal/githubauth` (GitHub App JWT → installation token), Prometheus
client. Tests need Postgres with pgvector from `docker-compose.yml`;
`store.OpenTestStore` skips silently when it is unreachable.

**Read first:**
- `docs/specs/004-execution-backbone.md` §5.2 (fact tables, release frontiers)
  and §5.3 (handlers and resolver, GitHub App requirements)
- `internal/hooks/github.go:464` (`applyRelease`) — the release path
- `internal/hooks/flux.go:175` (`revisionSHA`) and `:191`
  (`confirmFluxDelivery`) — the Flux path
- `internal/store/artifacts.go` — `CreateArtifact`,
  `ArtifactIDBySourceSHA`, `FindArtifactByImage`, `splitImage`
- `internal/store/delivery.go:110` (`LatestMainID`), `:136` (`MainIDForSHA`),
  `:157` (`MainIDForSHAAnyRepo`)
- `internal/hooks/github_test.go:1-120` — the `env` / `deliver` test harness

## Global Constraints

- **No new tables and no migration for artifact kinds.** `docker_image` is
  already in the `artifacts.kind` CHECK (`0001_baseline.up.sql:139`). Task 3
  adds one index migration and nothing else.
- **Never make an outbound HTTP call inside an apply callback.** The callback
  passed to `store.RecordEvent` runs inside an open `sql.Tx`; a GitHub API
  call there holds a database transaction open across the network. Resolve
  before opening the transaction and pass the value in.
- **A correlation must never fail a delivery.** Every lookup added here
  returns "no match" as a nil/empty value that the caller tolerates, exactly
  as `ArtifactIDBySourceSHA` and `MainIDForSHAAnyRepo` already do. A failed
  correlation degrades the artifact link; it must not roll back the event.
- **Metrics are required for the new outbound call** (spec 022): nil-safe
  metrics struct in the owning package's `metrics.go`, `worklode_` prefix,
  bounded label values, `prometheus.Registerer` threaded from `serve.go`.
- **Commit format:** describe the defect and the fix, not the plan file.
  Never add `Co-authored-by:` trailers.

---

## Decision required before Task 3

Spec 004 is `status: accepted`, and §5.3 enumerates both the handled webhook
events and the GitHub App permissions the backbone requires. Task 3 adds a
seventh handled event (`registry_package`) and needs **Packages: read** plus a
`registry_package` webhook subscription. That is a change to an accepted
spec's prose, which `docs/authoring-design-docs.md` routes through the
amend/supersede machinery rather than an in-place edit.

The plan does not decide this. Whoever accepts the plan picks one:

- **(a)** Edit 004 §5.3's event list and App-permission paragraph in place,
  treating the addition as an editorial extension of a list the spec already
  frames as open (the anchor freeze is about renumbering, not prose).
- **(b)** Raise the addition in a new spec section that `amends`
  `004-execution-backbone.md#sec-5.3`, with the mirroring `amendedBy` key in
  004.

Task 3 assumes **(a)** and includes the edit. If (b) is chosen, drop step 8
from Task 3 and land the spec change separately first.

---

## Task 1: Release artifacts carry a commit SHA, not a branch name

`applyRelease` writes `SourceSHA: p.Release.TargetCommitish` — which for a
UI-created tag is `"main"`, a branch name. `ArtifactIDBySourceSHA` then never
matches that artifact against any Flux revision, so the release artifact is
permanently uncorrelatable. The frontier logic three lines below already
resolves the same value to a real main commit; the artifact should use it.

**Files:**
- Modify: `internal/store/delivery.go` (add `MainSHAForID`)
- Modify: `internal/hooks/github.go:464-521` (`applyRelease`)
- Test: `internal/store/delivery_test.go`, `internal/hooks/github_test.go`

**Interfaces:**
- Consumes: `store.MainIDForSHA`, `store.LatestMainID` (existing)
- Produces: `store.MainSHAForID(tx *sql.Tx, mainID int64) (string, error)` —
  returns the `main_commits.sha` for an id, `""` if the row is gone. Task 4
  does not use it; the Flux path in Task 3 does not either.

- [ ] **Step 1: Write the failing store test**

In `internal/store/delivery_test.go`:

```go
func TestMainSHAForID(t *testing.T) {
	st := store.OpenTestStore(t)
	seedRepo(t, st, "sunstoneinstitute/demo")
	var id int64
	var sha string
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		got, err := store.AppendMainCommit(tx, "sunstoneinstitute/demo",
			"abc1230000000000000000000000000000000000", st.Now())
		if err != nil {
			return err
		}
		id = got
		sha, err = store.MainSHAForID(tx, id)
		return err
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
	if sha != "abc1230000000000000000000000000000000000" {
		t.Fatalf("MainSHAForID = %q, want the appended sha", sha)
	}
}

func TestMainSHAForIDUnknownIsEmpty(t *testing.T) {
	st := store.OpenTestStore(t)
	var sha string
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		sha, err = store.MainSHAForID(tx, 999999)
		return err
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
	if sha != "" {
		t.Fatalf("MainSHAForID(unknown) = %q, want empty", sha)
	}
}
```

Check the existing helper names in `internal/store/delivery_test.go` before
writing this — if the file seeds repos differently (or `AppendMainCommit` has
another name or signature), match the file, do not introduce a second style.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store -run TestMainSHAForID -v`
Expected: FAIL — `undefined: store.MainSHAForID`.

If it reports `SKIP` instead, Postgres is not reachable. Start it
(`docker compose up -d postgres`) — a skipped run proves nothing here.

- [ ] **Step 3: Add the store helper**

In `internal/store/delivery.go`, next to `MainIDForSHA`:

```go
// MainSHAForID returns the commit sha of a main_commits row, or "" if the
// id names no row. Callers that resolved an id from a frontier use it to
// recover the sha for artifact attribution.
func MainSHAForID(tx *sql.Tx, mainID int64) (string, error) {
	var sha string
	err := tx.QueryRow(`SELECT sha FROM main_commits WHERE id = $1`, mainID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("main sha for id %d: %w", mainID, err)
	}
	return sha, nil
}
```

- [ ] **Step 4: Run the store test to verify it passes**

Run: `go test ./internal/store -run TestMainSHAForID -v`
Expected: PASS (both cases).

- [ ] **Step 5: Write the failing handler test**

In `internal/hooks/github_test.go`, next to `TestReleaseFrontierNarrowsToTaggedCommit`:

```go
// TestReleaseArtifactUsesResolvedSHA: a release whose target_commitish is a
// branch name must store the commit the frontier resolved to, not the branch
// name — an artifact whose source_sha is "main" can never correlate to a
// Flux revision.
func TestReleaseArtifactUsesResolvedSHA(t *testing.T) {
	e := newEnv(t)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")
	head := "3333333333333333333333333333333333333333"

	// releaseBody (already in this file) builds a payload with an explicit
	// target_commitish — here the branch name a UI-created tag produces.
	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v2.0.0", "main"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	arts, err := e.st.ArtifactsBySourceSHA(context.Background(), head)
	if err != nil {
		t.Fatalf("artifacts by source sha: %v", err)
	}
	var found bool
	for _, a := range arts {
		if a.Kind == "git_tag" && a.Version == "v2.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no git_tag artifact for v2.0.0 at %s; got %+v", head, arts)
	}
}
```

Confirm `push_main_merge.json`'s head sha really is
`3333333333333333333333333333333333333333` (the existing
`TestReleaseSetsFrontier` asserts against it) before relying on the constant.

- [ ] **Step 6: Run the handler test to verify it fails**

Run: `go test ./internal/hooks -run TestReleaseArtifactUsesResolvedSHA -v`
Expected: FAIL — no artifact found, because `source_sha` is the literal
`"main"`.

- [ ] **Step 7: Reorder `applyRelease` so the artifact uses the resolved commit**

In `internal/hooks/github.go`, replace the body of `applyRelease` from the
`CreateArtifact` call through the frontier resolution with:

```go
	now := h.st.Now()
	publishedAt := p.Release.PublishedAt
	if publishedAt.IsZero() {
		publishedAt = now
	}

	// Resolve the release frontier first, so the artifact can be attributed
	// to a real commit. Prefer the tagged commit itself, so a backport tag
	// covers only what it actually contains. target_commitish is often a
	// branch name (UI-created tags) rather than a sha, which does not
	// resolve; the release then covers main's head as of this webhook's
	// arrival, which is right for release-on-merge.
	frontier, err := store.MainIDForSHA(tx, repo, p.Release.TargetCommitish)
	if err != nil {
		return err
	}
	if frontier == nil {
		if frontier, err = store.LatestMainID(tx, repo); err != nil {
			return err
		}
	}

	// The artifact's source_sha must be a commit, never a branch name: a
	// branch name can never match a Flux revision, so the artifact would be
	// permanently uncorrelatable. An unresolvable target_commitish leaves it
	// empty rather than wrong.
	sourceSHA := ""
	if frontier != nil {
		if sourceSHA, err = store.MainSHAForID(tx, *frontier); err != nil {
			return err
		}
	}
	if _, err := store.CreateArtifact(tx, store.Artifact{
		Kind:      "git_tag",
		Name:      repo,
		Version:   p.Release.TagName,
		Repo:      repo,
		SourceSHA: sourceSHA,
		BuiltAt:   p.Release.PublishedAt,
	}); err != nil {
		return err
	}

	if frontier == nil {
		return nil
	}
	if err := store.SetReleaseFrontier(tx, repo, p.Release.TagName, *frontier, publishedAt); err != nil {
		return err
	}
```

Leave the `TasksBelowFrontier` loop that follows unchanged.

- [ ] **Step 8: Run the hooks tests to verify they pass**

Run: `go test ./internal/hooks -run TestRelease -v`
Expected: PASS for all five `TestRelease*` tests.

`TestReleasePublished` asserts the artifact exists at
`abc1230000000000000000000000000000000000` with no main commits seeded at
all — under the new code `frontier` is nil there, so `source_sha` is now
empty and `ArtifactsBySourceSHA` returns nothing. Update that assertion to
look the artifact up by kind/name/version instead (add a small
`rawQueryString` helper if the test file has none), and extend its comment to
say why an uncorrelatable release stores no source sha. Do **not** weaken the
new test to accommodate the old one.

- [ ] **Step 9: Run the full package suites**

Run: `go build ./... && gofmt -l internal/ && go vet ./... && go test ./internal/store/... ./internal/hooks/...`
Expected: PASS, `gofmt -l` silent.

- [ ] **Step 10: Commit**

```bash
git add internal/store/delivery.go internal/store/delivery_test.go \
        internal/hooks/github.go internal/hooks/github_test.go
git commit -m "Attribute release artifacts to a commit, not a branch name

applyRelease stored target_commitish verbatim in artifacts.source_sha, so a
UI-created tag left the artifact holding \"main\". ArtifactIDBySourceSHA never
matches that, making every such release artifact uncorrelatable. Resolve the
frontier first and store the commit it names."
```

---

## Task 2: `registry_package` webhooks mint `docker_image` artifacts

No code path creates a `docker_image` artifact today, so
`FindArtifactByImage` (used by `InsertRuntimeEvent` to attribute crash-loops)
and the Flux digest correlation in Task 3 have nothing to find. GitHub emits
`registry_package` when a container image is pushed to GHCR.

**Files:**
- Create: `internal/hooks/testdata/github/registry_package_published.json`
- Modify: `internal/hooks/github.go` (`handledEvents`, `applyFunc`, new
  `applyRegistryPackage`)
- Modify: `docs/specs/004-execution-backbone.md` §5.3 (see "Decision required")
- Test: `internal/hooks/github_test.go`

**Interfaces:**
- Consumes: `store.CreateArtifact` (existing)
- Produces: nothing other tasks call directly. Task 3 relies on the artifacts
  this task writes having a non-empty `Digest` of the form `sha256:<hex>`.

- [ ] **Step 1: Confirm the payload shape against a real delivery**

The field paths below follow GitHub's documented `registry_package` schema,
but the event is legacy-flavoured and its container metadata has shifted
before. Every unhandled event is already recorded with its full body, so if
this instance has ever received one:

```sql
SELECT payload FROM events
 WHERE source = 'github' AND type LIKE 'registry_package%'
 ORDER BY id DESC LIMIT 1;
```

If a row exists, make the fixture in Step 2 a redacted copy of it. If none
exists, use the fixture as written and open the GitHub App's
Advanced → Deliveries page after the first real push to confirm. Record which
of the two you did in the commit message.

- [ ] **Step 2: Write the fixture**

`internal/hooks/testdata/github/registry_package_published.json`:

```json
{
  "action": "published",
  "repository": {"full_name": "sunstoneinstitute/demo"},
  "registry_package": {
    "name": "demo",
    "package_type": "CONTAINER",
    "package_version": {
      "version": "sha256:feed0000000000000000000000000000000000000000000000000000000000ff",
      "target_commitish": "abc1230000000000000000000000000000000000",
      "created_at": "2026-07-19T11:00:00Z",
      "package_url": "ghcr.io/sunstoneinstitute/demo",
      "container_metadata": {
        "tag": {
          "name": "v1.2.3",
          "digest": "sha256:feed0000000000000000000000000000000000000000000000000000000000ff"
        }
      }
    }
  }
}
```

- [ ] **Step 3: Write the failing test**

In `internal/hooks/github_test.go`:

```go
// TestRegistryPackagePublished: a container push mints a docker_image
// artifact keyed by image name and tag, carrying the OCI digest and the
// commit it was built from.
func TestRegistryPackagePublished(t *testing.T) {
	e := newEnv(t)
	deliverOK(t, e, "registry_package", "d-1", "registry_package_published.json")

	a, err := e.st.FindArtifactByImage(context.Background(),
		"ghcr.io/sunstoneinstitute/demo:v1.2.3")
	if err != nil {
		t.Fatalf("find artifact by image: %v", err)
	}
	if a.Kind != "docker_image" {
		t.Fatalf("kind = %q, want docker_image", a.Kind)
	}
	if a.Digest == nil || *a.Digest != "sha256:feed0000000000000000000000000000000000000000000000000000000000ff" {
		t.Fatalf("digest = %v, want the sha256 digest", a.Digest)
	}
	if a.SourceSHA != "abc1230000000000000000000000000000000000" {
		t.Fatalf("source_sha = %q, want the target commitish", a.SourceSHA)
	}
	if a.Repo != "sunstoneinstitute/demo" {
		t.Fatalf("repo = %q", a.Repo)
	}
	if a.BuiltAt.IsZero() {
		t.Fatal("built_at is zero")
	}
}

// TestRegistryPackageUntaggedIsRecordedNotApplied: a package version with no
// container tag has no (name, version) key to store under, so it is recorded
// as an event with no artifact rather than failing the delivery.
func TestRegistryPackageUntaggedIsRecordedNotApplied(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"registry_package": {
			"name": "demo",
			"package_type": "CONTAINER",
			"package_version": {
				"version": "sha256:beef",
				"package_url": "ghcr.io/sunstoneinstitute/demo"
			}
		}
	}`)
	rr := deliverBody(t, e.h, "registry_package", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifacts`); n != 0 {
		t.Fatalf("artifacts = %d, want 0", n)
	}
}

// TestRegistryPackageNonContainerIgnored: only container packages become
// docker_image artifacts; a npm/nuget package version is recorded and
// otherwise ignored.
func TestRegistryPackageNonContainerIgnored(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"registry_package": {
			"name": "demo",
			"package_type": "NPM",
			"package_version": {"version": "1.0.0"}
		}
	}`)
	rr := deliverBody(t, e.h, "registry_package", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifacts`); n != 0 {
		t.Fatalf("artifacts = %d, want 0", n)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/hooks -run TestRegistryPackage -v`
Expected: FAIL — `TestRegistryPackagePublished` reports the image is not
found, because `registry_package` is not in `handledEvents` and no apply runs.
The other two may pass vacuously; that is fine, they are regression fences.

- [ ] **Step 5: Route the event**

In `internal/hooks/github.go`, extend `handledEvents` — the comment above it
says it is the single source of truth and that the add-repo subscription
check reads it, so the comment's "eighth event" wording needs updating too:

```go
// handledEvents are the GitHub event names applyFunc routes. It is the single
// source of truth: applyFunc switches over these names, and the add-repo
// subscription check compares an installation's subscriptions against them, so
// adding a ninth event cannot leave the check behind.
var handledEvents = []string{
	"issues", "push", "pull_request", "deployment_status",
	"pull_request_review", "workflow_run", "release", "registry_package",
}
```

Add the case to `applyFunc`, alongside `release`:

```go
	case "registry_package":
		if env.Action != "published" && env.Action != "updated" {
			return nil
		}
		return func(tx *sql.Tx, _ int64) error {
			return h.applyRegistryPackage(tx, repo, body)
		}
```

- [ ] **Step 6: Write the apply**

Add to `internal/hooks/github.go`, after `applyRelease`:

```go
// applyRegistryPackage mints a docker_image artifact from a container push.
// The artifact is keyed by (image name, tag) so FindArtifactByImage and the
// Flux digest correlation can both reach it; a version with no container tag
// has no key to store under and is recorded as an event only.
func (h *githubHandler) applyRegistryPackage(tx *sql.Tx, repo string, body []byte) error {
	var p struct {
		RegistryPackage struct {
			Name           string `json:"name"`
			PackageType    string `json:"package_type"`
			PackageVersion struct {
				Version           string    `json:"version"`
				TargetCommitish   string    `json:"target_commitish"`
				CreatedAt         time.Time `json:"created_at"`
				PackageURL        string    `json:"package_url"`
				ContainerMetadata struct {
					Tag struct {
						Name   string `json:"name"`
						Digest string `json:"digest"`
					} `json:"tag"`
				} `json:"container_metadata"`
			} `json:"package_version"`
		} `json:"registry_package"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse registry_package payload: %w", err)
	}
	pkg := p.RegistryPackage
	if !strings.EqualFold(pkg.PackageType, "CONTAINER") &&
		!strings.EqualFold(pkg.PackageType, "docker") {
		return nil
	}
	tag := pkg.PackageVersion.ContainerMetadata.Tag.Name
	if tag == "" {
		// An untagged push (digest-only) has no artifact key. Recording the
		// event is the whole effect; a later tagged push carries the same
		// digest.
		return nil
	}

	// The image name must match what a Kubernetes image reference says, so
	// splitImage's (name, tag) split lines up: prefer package_url, which is
	// the registry-qualified name.
	name := pkg.PackageVersion.PackageURL
	if name == "" {
		name = pkg.Name
	}
	digest := pkg.PackageVersion.ContainerMetadata.Tag.Digest
	if digest == "" {
		digest = pkg.PackageVersion.Version
	}
	var digestPtr *string
	if strings.HasPrefix(digest, "sha256:") {
		digestPtr = &digest
	}
	builtAt := pkg.PackageVersion.CreatedAt
	if builtAt.IsZero() {
		builtAt = h.st.Now()
	}

	_, err := store.CreateArtifact(tx, store.Artifact{
		Kind:      "docker_image",
		Name:      name,
		Version:   tag,
		Digest:    digestPtr,
		Repo:      repo,
		SourceSHA: pkg.PackageVersion.TargetCommitish,
		BuiltAt:   builtAt,
	})
	return err
}
```

`target_commitish` on a package version is a commit SHA when the push came
from a workflow run. If it arrives as a branch name it is stored as-is and
simply fails to correlate — the same degradation Task 1 removed from the
release path. Leave it: unlike a release, a package version has no frontier
to resolve against, and Task 4's resolver is release-specific.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/hooks -run TestRegistryPackage -v`
Expected: PASS (all three).

- [ ] **Step 8: Record the event in spec 004 §5.3**

Only if option (a) from "Decision required" was chosen. In
`docs/specs/004-execution-backbone.md` §5.3, add to the handler list, after
the `release` bullet:

```markdown
- **`registry_package`** (new): a published container version mints a
  `docker_image` artifact keyed by image name and tag, carrying the OCI
  digest. Non-container package types and untagged (digest-only) versions are
  recorded as events with no artifact. It resolves no frontier and calls no
  resolver: an image is a build fact, and delivery still turns on the deploy
  and release frontiers.
```

And extend the GitHub App requirements paragraph in the same section:
`**Packages: read** (`registry_package` webhook events)`, with
`registry_package` added to the list of webhook subscriptions.

Then run the doc checks:

```bash
./scripts/secfmt.py -l
./scripts/secmeta.py
```

Expected: both silent. They are report-only and never rewrite, so a complaint
is yours to fix by hand.

- [ ] **Step 9: Run the full suites**

Run: `go build ./... && gofmt -l internal/ && go vet ./... && go test ./internal/hooks/... ./internal/api/...`
Expected: PASS. `internal/api` is in scope because `admin.go:436` compares
`hooks.HandledEvents()` against the App's subscriptions — a test asserting the
old seven-event list will fail here and should be updated to the new list.

- [ ] **Step 10: Commit**

```bash
git add internal/hooks/github.go internal/hooks/github_test.go \
        internal/hooks/testdata/github/registry_package_published.json \
        docs/specs/004-execution-backbone.md
git commit -m "Mint docker_image artifacts from registry_package webhooks

Nothing created docker_image artifacts, so FindArtifactByImage never resolved
a running image and Flux OCI revisions had nothing to correlate against.
Ingest registry_package: a tagged container version becomes an artifact keyed
by image name and tag, carrying the OCI digest."
```

---

## Task 3: Flux correlates OCI-digest revisions through the artifact

`revisionSHA` treats every revision as a git ref. An OCIRepository revision is
`<tag>@sha256:<hex>`, so the function returns `"sha256:<hex>"` and hands it to
`MainIDForSHAAnyRepo` and `ArtifactIDBySourceSHA`, both of which look for a
git commit and find nothing. The revision is silently dropped. With Task 2's
artifacts in place, a digest resolves to an artifact, and the artifact's
`source_sha` resolves to the main commit the deploy actually delivered.

**Files:**
- Create: `deploy/base/migrations/00NN_artifact_digest_index.up.sql` / `.down.sql`
- Modify: `deploy/base/kustomization.yaml`
- Modify: `internal/store/artifacts.go` (add `ArtifactByDigest`)
- Modify: `internal/hooks/flux.go` (`revisionSHA` → `parseRevision`, `apply`,
  `confirmFluxDelivery`)
- Create: `internal/hooks/testdata/flux/kustomization_succeeded_oci.json`
- Test: `internal/store/artifacts_test.go`, `internal/hooks/flux_test.go`

**Interfaces:**
- Consumes: `store.ArtifactIDBySourceSHA`, `store.MainIDForSHAAnyRepo`,
  `store.BumpEnvDeployFlux`, `resolveFrontier` (all existing)
- Produces:
  - `store.ArtifactByDigest(tx *sql.Tx, digest string) (*store.Artifact, error)`
    — newest artifact with that digest, `nil` if none.
  - `hooks.parseRevision(revision string) (gitSHA, ociDigest string)` —
    package-private; exactly one of the two is non-empty, both empty for an
    unrecognised or empty revision.

- [ ] **Step 1: Add the digest index migration**

Run the collision check's own numbering: take the next free number after the
highest in `deploy/base/migrations/` (0017 at time of writing, so 0018).

`deploy/base/migrations/0018_artifact_digest_index.up.sql`:

```sql
-- Flux OCI revisions correlate by digest, which is otherwise an unindexed
-- scan of every artifact on every reconciliation event.
CREATE INDEX artifacts_digest_idx ON artifacts (digest) WHERE digest IS NOT NULL;
```

`deploy/base/migrations/0018_artifact_digest_index.down.sql`:

```sql
DROP INDEX IF EXISTS artifacts_digest_idx;
```

Add both to the `resources` list in `deploy/base/kustomization.yaml`, matching
how `0017_narrow_task_kinds` is listed there.

- [ ] **Step 2: Run the migration collision check**

Run: `./scripts/check-migrations.sh --no-fix`
Expected: no collision reported. If it reports one, another branch claimed
0018 — renumber to the next free number and re-run.

- [ ] **Step 3: Write the failing store test**

In `internal/store/artifacts_test.go`, next to `TestArtifactIDBySourceSHANewestWins`:

```go
func TestArtifactByDigest(t *testing.T) {
	st := store.OpenTestStore(t)
	digest := "sha256:feed00"
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := store.CreateArtifact(tx, store.Artifact{
			Kind: "docker_image", Name: "ghcr.io/demo", Version: "v1",
			Digest: &digest, Repo: "sunstoneinstitute/demo",
			SourceSHA: "abc123", BuiltAt: st.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	var got *store.Artifact
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		got, err = store.ArtifactByDigest(tx, digest)
		return err
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
	if got == nil || got.SourceSHA != "abc123" {
		t.Fatalf("ArtifactByDigest = %+v, want the seeded artifact", got)
	}
}

func TestArtifactByDigestNoneFound(t *testing.T) {
	st := store.OpenTestStore(t)
	var got *store.Artifact
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		got, err = store.ArtifactByDigest(tx, "sha256:absent")
		return err
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
	if got != nil {
		t.Fatalf("ArtifactByDigest = %+v, want nil", got)
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/store -run TestArtifactByDigest -v`
Expected: FAIL — `undefined: store.ArtifactByDigest`.

- [ ] **Step 5: Add the store helper**

In `internal/store/artifacts.go`, after `ArtifactIDBySourceSHA`:

```go
// ArtifactByDigest looks up an artifact by its OCI digest inside the given
// transaction, for callers (the Flux webhook) that must resolve it
// atomically with the rest of their apply. Returns nil if no artifact
// matches. Several artifacts can share a digest (the same image published
// under two tags); the newest one (highest id) wins.
func ArtifactByDigest(tx *sql.Tx, digest string) (*Artifact, error) {
	row := tx.QueryRow(
		`SELECT `+artifactColumns+` FROM artifacts WHERE digest = $1 ORDER BY id DESC LIMIT 1`,
		digest)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifact by digest %s: %w", digest, err)
	}
	return a, nil
}
```

- [ ] **Step 6: Run it to verify it passes**

Run: `go test ./internal/store -run TestArtifactByDigest -v`
Expected: PASS (both cases).

- [ ] **Step 7: Write the failing Flux tests**

`internal/hooks/testdata/flux/kustomization_succeeded_oci.json`:

```json
{
  "involvedObject": {"kind": "Kustomization", "namespace": "flux-system", "name": "demo"},
  "severity": "info",
  "timestamp": "2026-07-19T10:00:00Z",
  "message": "Applied revision: v1.2.3@sha256:feed0000000000000000000000000000000000000000000000000000000000ff",
  "reason": "ReconciliationSucceeded",
  "metadata": {
    "revision": "v1.2.3@sha256:feed0000000000000000000000000000000000000000000000000000000000ff",
    "cluster": "prod-1"
  }
}
```

In `internal/hooks/flux_test.go`:

```go
func TestParseRevision(t *testing.T) {
	for _, tc := range []struct {
		name, revision, git, oci string
	}{
		{"git with branch", "main@sha1:abc123", "abc123", ""},
		{"git bare sha1 prefix", "sha1:abc123", "abc123", ""},
		{"git bare sha", "abc123", "abc123", ""},
		{"oci tag and digest", "v1.2.3@sha256:feed00", "", "sha256:feed00"},
		{"oci digest only", "sha256:feed00", "", "sha256:feed00"},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git, oci := hooks.ParseRevisionForTest(tc.revision)
			if git != tc.git || oci != tc.oci {
				t.Fatalf("parseRevision(%q) = (%q, %q), want (%q, %q)",
					tc.revision, git, oci, tc.git, tc.oci)
			}
		})
	}
}
```

`flux_test.go` is in package `hooks_test`, so add an export shim in a new
`internal/hooks/export_test.go` (package `hooks`):

```go
package hooks

// ParseRevisionForTest exposes parseRevision to the external test package.
var ParseRevisionForTest = parseRevision
```

Check first whether `internal/hooks` already has an export shim file or an
in-package test — if `revisionSHA` is currently tested in package `hooks`,
follow that instead of adding a second mechanism.

Then the correlation test:

```go
// TestFluxOCIRevisionCorrelatesThroughArtifact: an OCIRepository revision is
// a digest, not a git sha. It resolves to the docker_image artifact carrying
// that digest, and through the artifact's source_sha to the main commit the
// deployment actually delivered.
func TestFluxOCIRevisionCorrelatesThroughArtifact(t *testing.T) {
	e := newFluxEnv(t)
	sha := "abc1230000000000000000000000000000000000"
	digest := "sha256:feed0000000000000000000000000000000000000000000000000000000000ff"
	seedMainCommit(t, e.st, demoRepo, sha)
	seedArtifact(t, e.st, store.Artifact{
		Kind: "docker_image", Name: "ghcr.io/sunstoneinstitute/demo",
		Version: "v1.2.3", Digest: &digest, Repo: demoRepo, SourceSHA: sha,
	})

	deliverFluxOK(t, e, "kustomization_succeeded_oci.json")

	got := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM deployments d
		   JOIN artifacts a ON a.id = d.artifact_id
		  WHERE a.digest = $1`, digest)
	if got != 1 {
		t.Fatalf("deployments linked to the digest artifact = %d, want 1", got)
	}
}
```

Match the existing helper names in `flux_test.go` — `newFluxEnv`,
`deliverFluxOK`, `demoRepo`, `rawQueryInt` are the names used by
`TestFluxKustomizationSucceededWithArtifact`; reuse whatever that test uses
rather than inventing seeds. Add `seedMainCommit` / `seedArtifact` only if no
equivalent exists.

- [ ] **Step 8: Run the Flux tests to verify they fail**

Run: `go test ./internal/hooks -run 'TestParseRevision|TestFluxOCI' -v`
Expected: FAIL — `undefined: hooks.ParseRevisionForTest`, and the correlation
test finds 0 linked deployments because `"sha256:feed…"` is looked up as a
`source_sha`.

- [ ] **Step 9: Split revision parsing by kind**

In `internal/hooks/flux.go`, replace `revisionSHA` with:

```go
// parseRevision classifies a Flux metadata.revision. A GitRepository
// revision names a commit ("main@sha1:<sha>", "sha1:<sha>", bare "<sha>");
// an OCIRepository revision names an image digest ("<tag>@sha256:<hex>",
// bare "sha256:<hex>"). Exactly one return value is non-empty; both are
// empty for an empty or unrecognised revision.
func parseRevision(revision string) (gitSHA, ociDigest string) {
	if revision == "" {
		return "", ""
	}
	if i := strings.LastIndex(revision, "sha256:"); i >= 0 {
		return "", revision[i:]
	}
	if i := strings.LastIndex(revision, "sha1:"); i >= 0 {
		return revision[i+len("sha1:"):], ""
	}
	if i := strings.LastIndex(revision, "@"); i >= 0 {
		return revision[i+1:], ""
	}
	return revision, ""
}
```

The `sha256:` check comes first: an OCI revision has no `sha1:` marker, and
checking `@` first would return the digest as if it were a git sha — the
current bug.

- [ ] **Step 10: Resolve the artifact by whichever kind the revision is**

Still in `flux.go`, replace the artifact lookup at the top of `apply`:

```go
	gitSHA, ociDigest := parseRevision(ev.Metadata["revision"])
	var artifactID *int64
	switch {
	case ociDigest != "":
		a, err := store.ArtifactByDigest(tx, ociDigest)
		if err != nil {
			return err
		}
		if a != nil {
			artifactID = &a.ID
		}
	case gitSHA != "":
		id, err := store.ArtifactIDBySourceSHA(tx, gitSHA)
		if err != nil {
			return err
		}
		artifactID = id
	}
```

and change the `ReconciliationSucceeded` branch's call from
`h.confirmFluxDelivery(tx, now, environment, ev.Metadata["revision"], eventID)`
to pass the parsed values:
`h.confirmFluxDelivery(tx, now, environment, gitSHA, ociDigest, eventID)`.

- [ ] **Step 11: Teach `confirmFluxDelivery` the digest path**

```go
// confirmFluxDelivery advances the Flux side of the deploy frontier for
// environment when the reconciled revision maps to a repo we track, then
// resolves the tasks the new frontier covers. An OCI digest reaches the
// commit through the artifact built from it; a git revision names the commit
// directly.
func (h *fluxHandler) confirmFluxDelivery(tx *sql.Tx, now time.Time, environment, gitSHA, ociDigest string, eventID int64) error {
	// clusterEnv is unvalidated operator config (LODE_CLUSTER_ENV_MAP), so
	// environment can be anything; env_deploys only accepts dev|prod and a
	// CHECK violation would abort the whole delivery.
	if environment != "dev" && environment != "prod" {
		return nil
	}
	sha := gitSHA
	if ociDigest != "" {
		a, err := store.ArtifactByDigest(tx, ociDigest)
		if err != nil {
			return err
		}
		if a == nil {
			// The image was deployed before its registry_package webhook
			// arrived, or from a registry we do not ingest. Latching on a
			// signal we cannot confirm would gate the repo/env forever.
			return nil
		}
		sha = a.SourceSHA
	}
	if sha == "" {
		return nil
	}
	repo, mainID, err := store.MainIDForSHAAnyRepo(tx, sha)
	...
}
```

Leave the rest of the function (the `mainID == nil` guard,
`BumpEnvDeployFlux`, the latch log line, `resolveFrontier`) exactly as it is.

- [ ] **Step 12: Run the Flux tests to verify they pass**

Run: `go test ./internal/hooks -v`
Expected: PASS, including the pre-existing
`TestFluxKustomizationSucceededWithArtifact` and
`TestFluxKustomizationSucceededNoArtifactMatch` — the git path must be
unchanged.

- [ ] **Step 13: Run the full suites with the new migration applied**

```bash
docker compose up -d migrate
go build ./... && gofmt -l internal/ && go vet ./...
go test ./internal/store/... ./internal/hooks/...
```

Expected: PASS. Store tests create and drop their own databases and run
migrations themselves — if `artifacts_digest_idx` is missing, the migration
was not added to `deploy/base/kustomization.yaml` or the test harness's
migration path.

- [ ] **Step 14: Commit**

```bash
git add deploy/base/migrations/0018_artifact_digest_index.up.sql \
        deploy/base/migrations/0018_artifact_digest_index.down.sql \
        deploy/base/kustomization.yaml \
        internal/store/artifacts.go internal/store/artifacts_test.go \
        internal/hooks/flux.go internal/hooks/flux_test.go \
        internal/hooks/export_test.go \
        internal/hooks/testdata/flux/kustomization_succeeded_oci.json
git commit -m "Correlate OCI Flux revisions through the image artifact

revisionSHA treated every revision as a git ref, so an OCIRepository revision
\"v1@sha256:...\" came back as \"sha256:...\" and matched neither a commit nor
an artifact — the deployment was silently uncorrelated. Classify the revision,
resolve a digest to its docker_image artifact, and reach the delivered commit
through the artifact's source_sha."
```

---

## Task 4: Resolve a branch `target_commitish` through the GitHub App

After Task 1 a release whose `target_commitish` is a branch other than the one
main is tracked on still resolves to main's head, which for a release branch
is wrong: the tag covers the release branch's tip, not main's. Resolving the
branch to a SHA needs a GitHub API call, which cannot happen inside the apply
transaction — so it happens in `ServeHTTP`, before `RecordEvent` opens one.

This is the only task that touches the server's wiring, and the only one that
adds an outbound call. It is last because everything before it is useful
without it.

**Files:**
- Modify: `internal/githubauth/app.go` (add `BranchSHA`)
- Modify: `internal/hooks/github.go` (`githubHandler` field, `NewGitHubHandler`
  signature, `ServeHTTP` pre-resolution, `applyRelease` signature)
- Modify: `internal/hooks/metrics.go`
- Modify: `internal/api/server.go:386` (pass `s.appAuth`)
- Test: `internal/githubauth/app_test.go`, `internal/hooks/github_test.go`

**Interfaces:**
- Consumes: `githubauth.AppAuth.InstallationToken` (existing),
  `store.MainIDForSHA` / `MainSHAForID` (Task 1)
- Produces:
  - `(*githubauth.AppAuth).BranchSHA(ctx context.Context, repo, branch string) (string, error)`
    — the head commit sha of a branch; `""` with a nil error when the branch
    does not exist (404).
  - `hooks.NewGitHubHandler(st, secret, log, onSkillPush, appAuth *githubauth.AppAuth, m *Metrics)`
    — one new parameter, before the metrics argument. `nil` is valid and
    disables resolution (every existing test passes `nil`).

- [ ] **Step 1: Write the failing `BranchSHA` test**

In `internal/githubauth/app_test.go`, following the httptest-server pattern
the existing `DiscoverDoneState` tests use (read them first — they stub both
the installation-token exchange and the API call against a `BaseURL`):

```go
func TestBranchSHA(t *testing.T) {
	srv := newFakeGitHub(t, map[string]string{
		"/repos/sunstoneinstitute/demo/git/ref/heads/release-1.2": `{
			"ref": "refs/heads/release-1.2",
			"object": {"sha": "abc1230000000000000000000000000000000000", "type": "commit"}
		}`,
	})
	a := newTestAppAuth(t, srv.URL)

	sha, err := a.BranchSHA(context.Background(), "sunstoneinstitute/demo", "release-1.2")
	if err != nil {
		t.Fatalf("BranchSHA: %v", err)
	}
	if sha != "abc1230000000000000000000000000000000000" {
		t.Fatalf("sha = %q", sha)
	}
}

func TestBranchSHAUnknownBranchIsEmpty(t *testing.T) {
	srv := newFakeGitHub(t, nil) // every path 404s
	a := newTestAppAuth(t, srv.URL)

	sha, err := a.BranchSHA(context.Background(), "sunstoneinstitute/demo", "nope")
	if err != nil {
		t.Fatalf("BranchSHA: %v", err)
	}
	if sha != "" {
		t.Fatalf("sha = %q, want empty for a 404", sha)
	}
}
```

`newFakeGitHub` and `newTestAppAuth` are placeholders for whatever the file
already uses. Do not add a second fake-server helper — reuse the existing one
and extend its route map.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/githubauth -run TestBranchSHA -v`
Expected: FAIL — `undefined: BranchSHA`.

- [ ] **Step 3: Implement `BranchSHA`**

In `internal/githubauth/app.go`, next to `DiscoverDoneState`:

```go
// BranchSHA returns the head commit sha of a branch, or "" when the branch
// does not exist. Callers correlating a release's target_commitish use it to
// turn a branch name into a commit; a missing branch is an ordinary outcome
// (the commitish was a tag, or the branch was deleted after the release), not
// an error.
func (a *AppAuth) BranchSHA(ctx context.Context, repo, branch string) (string, error) {
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return "", err
	}
	path, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	code, err := githubJSON(ctx, "GET",
		a.BaseURL+"/repos/"+path+"/git/ref/heads/"+url.PathEscape(branch),
		"Bearer "+token, &out)
	if err != nil {
		return "", fmt.Errorf("branch sha %s#%s: %w", repo, branch, err)
	}
	if code == http.StatusNotFound {
		return "", nil
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("branch sha %s#%s: status %d", repo, branch, code)
	}
	return out.Object.SHA, nil
}
```

Match `DiscoverDoneState`'s exact call convention — if `githubJSON` in this
package returns `(status, error)` in a different order or takes different
arguments, follow the existing call site, not this snippet.

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/githubauth -run TestBranchSHA -v`
Expected: PASS (both cases).

- [ ] **Step 5: Add the metric**

In `internal/hooks/metrics.go`, following the nil-safe pattern already in that
file:

```go
	// branchResolve counts GitHub API calls made to turn a release's
	// target_commitish branch name into a commit sha. outcome is one of
	// "resolved", "unknown" (branch does not exist), "error", or "skipped"
	// (no App configured).
	branchResolve *prometheus.CounterVec
```

registered as:

```go
	m.branchResolve = promauto.With(r).NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_github_branch_resolve_total",
		Help: "GitHub branch-to-commit resolutions attempted by the release webhook, by outcome.",
	}, []string{"outcome"})
```

with the nil-safe accessor matching the file's existing `event` method:

```go
func (m *Metrics) branchResolved(outcome string) {
	if m == nil || m.branchResolve == nil {
		return
	}
	m.branchResolve.WithLabelValues(outcome).Inc()
}
```

The four outcome values are the complete bounded set — never pass a branch
name or repo as a label.

- [ ] **Step 6: Write the failing handler test**

In `internal/hooks/github_test.go`:

```go
// TestReleaseResolvesBranchCommitish: a release cut from a release branch
// resolves that branch to its head commit through the GitHub App, so the
// frontier and the artifact name the branch tip rather than main's head.
func TestReleaseResolvesBranchCommitish(t *testing.T) {
	e := newEnvWithBranchResolver(t, func(repo, branch string) (string, error) {
		if repo == demoRepo && branch == "release-1.2" {
			return "9999999999999999999999999999999999999999", nil
		}
		return "", nil
	})
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v1.2.4", "release-1.2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	arts, err := e.st.ArtifactsBySourceSHA(context.Background(),
		"9999999999999999999999999999999999999999")
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %v, err = %v, want 1", arts, err)
	}
}

// TestReleaseUnresolvableBranchFallsBackToMainHead: with no App configured
// the handler keeps the release-on-merge fallback rather than failing.
func TestReleaseUnresolvableBranchFallsBackToMainHead(t *testing.T) {
	e := newEnv(t) // nil resolver
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v1.2.4", "release-1.2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v1.2.4'`, demoRepo)
	if got != e.mainCommitID(t, "3333333333333333333333333333333333333333") {
		t.Fatalf("frontier = %d, want main head", got)
	}
}
```

The handler must take a **function**, not an `*AppAuth`, so the test can stub
it without a fake HTTP server: define the seam as
`resolveBranch func(ctx context.Context, repo, branch string) (string, error)`
on `githubHandler`, and have `NewGitHubHandler` build it from the `*AppAuth`
argument (nil App → nil resolver). `newEnvWithBranchResolver` is a new helper
that mirrors `newEnv` but passes one in; refactor `newEnv` to call it with
`nil` rather than duplicating the body.

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/hooks -run 'TestReleaseResolves|TestReleaseUnresolvable' -v`
Expected: FAIL — `undefined: newEnvWithBranchResolver`.

- [ ] **Step 8: Pre-resolve in `ServeHTTP`**

In `internal/hooks/github.go`, add the field and constructor parameter, then
resolve **before** `RecordEvent`. In `ServeHTTP`, after the `ignored` lookup
and before building `apply`:

```go
	// A release's target_commitish is often a branch name. Resolving it needs
	// a GitHub API call, which must not happen inside the apply callback —
	// that runs in an open transaction. Resolve here and pass the sha in; a
	// failure degrades to the existing main-head fallback and never fails the
	// delivery.
	resolvedCommitish := ""
	if event == "release" && env.Action == "published" && !ignored {
		resolvedCommitish = h.resolveReleaseCommitish(r.Context(), env.Repository.FullName, body)
	}
```

and the helper:

```go
// resolveReleaseCommitish turns a release's target_commitish into a commit
// sha when it names a branch. Returns "" when there is nothing to resolve, no
// App is configured, or the lookup fails — every one of which leaves
// applyRelease on its existing fallback.
func (h *githubHandler) resolveReleaseCommitish(ctx context.Context, repo string, body []byte) string {
	var p struct {
		Release struct {
			TargetCommitish string `json:"target_commitish"`
		} `json:"release"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	commitish := p.Release.TargetCommitish
	if commitish == "" || isCommitSHA(commitish) {
		return ""
	}
	if h.resolveBranch == nil {
		h.metrics.branchResolved("skipped")
		return ""
	}
	sha, err := h.resolveBranch(ctx, repo, commitish)
	switch {
	case err != nil:
		h.log.Warn("release target_commitish resolution failed",
			"repo", repo, "branch", commitish, "err", err)
		h.metrics.branchResolved("error")
		return ""
	case sha == "":
		h.metrics.branchResolved("unknown")
		return ""
	default:
		h.metrics.branchResolved("resolved")
		return sha
	}
}

// isCommitSHA reports whether s is a full 40-character hex commit sha, the
// form target_commitish takes when a release was cut from an explicit commit.
func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
```

Thread `resolvedCommitish` into `applyFunc` → the `release` case →
`applyRelease(tx, eventID, repo, body, resolvedCommitish)`.

- [ ] **Step 9: Use the resolved sha in `applyRelease`**

Change the frontier resolution added in Task 1 to prefer it:

```go
	// A pre-resolved sha (ServeHTTP turned a branch name into a commit)
	// takes precedence; otherwise use the payload's own commitish.
	commitish := resolvedCommitish
	if commitish == "" {
		commitish = p.Release.TargetCommitish
	}
	frontier, err := store.MainIDForSHA(tx, repo, commitish)
```

Everything downstream — the `LatestMainID` fallback, `MainSHAForID`, the
artifact, `SetReleaseFrontier` — is unchanged. A release-branch tip that has
not landed on main still resolves to nothing and falls back, which is correct:
the backbone tracks delivery through main.

- [ ] **Step 10: Wire the App in**

`internal/api/server.go:386`:

```go
	r.public("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log, onSkillPush, s.appAuth, hookMetrics))
```

`s.appAuth` is already nil when no App is configured (`newAppAuth`, `:114`),
which is exactly the "skipped" path.

- [ ] **Step 11: Run everything**

```bash
go build ./... && gofmt -l internal/ && go vet ./...
go test ./internal/githubauth/... ./internal/hooks/... ./internal/api/...
go test -race -count=1 -tags e2e ./e2e/
```

Expected: PASS throughout, `gofmt -l` silent. Every existing
`NewGitHubHandler` call site (tests and `e2e/`) needs the new `nil` argument —
the compiler finds them all.

- [ ] **Step 12: Record the resolution in spec 004 §5.2**

§5.2's `release_frontiers` paragraph currently states that a branch-name
`target_commitish` "does not resolve" and always falls back to main's head.
That is no longer the whole truth. Under option (a) from "Decision required",
amend the paragraph:

```markdown
`target_commitish` is often a branch name (UI-created tags); the handler
resolves it to that branch's head commit through the GitHub App before
opening the delivery transaction, and falls back to main's head as of the
webhook's arrival when no App is configured or the branch cannot be
resolved — right for release-on-merge.
```

Run `./scripts/secfmt.py -l` and `./scripts/secmeta.py`; both must be silent.

- [ ] **Step 13: Commit**

```bash
git add internal/githubauth/app.go internal/githubauth/app_test.go \
        internal/hooks/github.go internal/hooks/github_test.go \
        internal/hooks/metrics.go internal/api/server.go \
        docs/specs/004-execution-backbone.md
git commit -m "Resolve a release's branch target_commitish to a commit

A release cut from a release branch resolved to main's head, so its frontier
and artifact named a commit the tag does not contain. Resolve the branch
through the GitHub App before the delivery transaction opens — an API call
must never run inside RecordEvent's tx — and keep the main-head fallback for
instances with no App configured."
```

---

## Verification

After all four tasks, the chain the follow-up called broken should connect
end to end. Prove it, do not assert it:

```bash
docker compose up -d
go build ./... && gofmt -l internal/ && go vet ./...
go test ./... && go test -race -count=1 -tags e2e ./e2e/
./scripts/check-migrations.sh --no-fix
./scripts/secfmt.py -l && ./scripts/secmeta.py
```

Store and hooks tests skip silently when Postgres is unreachable. State
explicitly, when reporting the work, whether Postgres was actually up for the
run — a green suite without it proves almost nothing about this change.

## Follow-ups this plan deliberately does not close

- **Per-artifact delivery tracking.** 004 §5.3 already defers it: a repo
  shipping two images plus a CLI binary still has one `done_state`. Task 2
  mints all of them as artifacts, but delivery is still decided per repo.
- **`registry_package` for non-container packages.** PyPI/npm versions are
  recorded as events and ignored. The `pypi` artifact kind exists in the
  CHECK; nothing writes it, and nothing reads it either.
- **`target_commitish` on a package version.** Task 2 stores it verbatim, so
  a branch name there stays uncorrelated. Unlike a release it has no frontier
  to fall back on; give it Task 4's resolver only if real deliveries show it
  arriving as a branch.
