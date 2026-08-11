---
status: draft
covers: NO-SPEC
---
# Worklode Scoop Bucket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Windows distribution channel for `lode` that mirrors the
Homebrew tap — a [Scoop](https://scoop.sh) bucket at
`sunstoneinstitute/scoop-bucket`, updated automatically on every release, so a
Windows user installs with:

```powershell
scoop bucket add sunstone https://github.com/sunstoneinstitute/scoop-bucket
scoop install sunstone/worklode
```

(`sunstone` is the user's local alias for the bucket; it is a `scoop bucket add`
argument only and appears nowhere in the manifest or the workflows.)

**Architecture:** Mirror the bottle pipeline one-for-one. The one structural
difference: Homebrew builds `lode` from source on the user's Mac (bottles are
prebuilt fallbacks in Homebrew's own format), whereas Scoop downloads a
prebuilt artifact and verifies a hash. So Scoop needs something the macOS path
never produced — an actual `lode.exe`.

- A new reusable workflow `_build-windows.yml` cross-compiles `lode.exe` on
  `ubuntu-latest` (pure Go, `CGO_ENABLED=0` — no Windows runner, seconds not
  minutes), zips it, attaches the zip to the tag's GitHub Release, and outputs
  the zip's sha256.
- A new composite action `update-scoop-bucket` (parallel to
  `update-homebrew-tap`) renders `bucket/worklode.json` from a shared template
  and pushes it to `sunstoneinstitute/scoop-bucket` over a write-enabled deploy
  key, using the same hardened SSH setup the tap action already uses.
- Both `release.yml` (hand-pushed `v*` tag) and `promote-prod.yml`
  (token-pushed tag that never triggers `release.yml`) gain a `build-windows`
  job and a `scoop` job, added alongside the existing `build-bottles`/`homebrew`
  jobs so the two channels ship together from one tag.

**Tech Stack:** GitHub Actions (Ubuntu runner), Go cross-compilation
(`GOOS=windows GOARCH=amd64`), Python 3 (template rendering, matching
`.github/homebrew/`), `zip`/`sha256sum`, Scoop JSON manifest.

**Scope:** amd64 only. `lode` is pure Go, so a Windows-on-ARM (`arm64`) block is
a later one-line addition if demand appears; it is deliberately omitted now.

**Not in scope / no metrics:** this is release/CI wiring with no server code, no
new HTTP endpoint, background loop, or store operation — the `worklode_*`
metrics rule does not apply.

---

## Prerequisite (one-time, human-run before the first Scoop release)

The `scoop` job pushes to `sunstoneinstitute/scoop-bucket` over an SSH deploy
key exposed as `SCOOP_DEPLOY_KEY` in this repo's `release` GitHub environment —
exactly mirroring `TAP_DEPLOY_KEY`. Task 6 produces a guided wizard
(`scripts/setup-scoop-deploy-key.sh`) that:

1. generates an ed25519 keypair,
2. walks the operator through adding the **public** half as a **write-enabled**
   deploy key on `sunstoneinstitute/scoop-bucket`
   (`https://github.com/sunstoneinstitute/scoop-bucket/settings/keys`),
3. sets the **private** half as the `release`-environment secret via
   `gh secret set SCOOP_DEPLOY_KEY --env release -R sunstoneinstitute/worklode`,
4. deletes the local key files.

Until this secret exists, the `scoop` job fails the same way a missing
`TAP_DEPLOY_KEY` fails the `homebrew` job (the action's `ssh-keygen -y`
validation reports "not a usable OpenSSH private key"). The rest of the release
is unaffected.

---

## File Structure

New files:

```
.github/scoop/worklode.json.template     # Scoop manifest with __PLACEHOLDER__ tokens
.github/scoop/render-manifest.py         # fills the template, validates the result
.github/workflows/_build-windows.yml     # reusable: build lode.exe, zip, attach, output sha
.github/actions/update-scoop-bucket/action.yml   # render manifest + push to bucket repo
scripts/setup-scoop-deploy-key.sh        # one-time deploy-key wizard
```

Edited files:

```
.github/workflows/release.yml            # + build-windows + scoop jobs
.github/workflows/promote-prod.yml       # + build-windows + scoop jobs
README.md                                # Quickstart: package-manager install note
```

---

## Task 1 — Scoop manifest template + render script

**Create `.github/scoop/worklode.json.template`:**

```json
{
    "version": "__VERSION__",
    "description": "Work tracker CLI (lode) for Sunstone Institute",
    "homepage": "https://github.com/sunstoneinstitute/worklode",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "__URL__",
            "hash": "__SHA256__",
            "bin": "lode.exe"
        }
    },
    "checkver": "github",
    "autoupdate": {
        "architecture": {
            "64bit": {
                "url": "https://github.com/sunstoneinstitute/worklode/releases/download/v$version/lode_$version_windows_amd64.zip"
            }
        }
    }
}
```

- `checkver`/`autoupdate` are idiomatic Scoop and let a human bump the manifest
  by hand later. They never fire automatically, so they do not conflict with the
  CI render. `$version` is Scoop's own token (not a `__…__` placeholder), so the
  render script leaves it untouched.
- `license` matches the repo's `LICENSE` (MIT).

**Create `.github/scoop/render-manifest.py`** — same shape and validation
discipline as `.github/homebrew/render-formula.py`:

```python
import json
import os
import sys

template_path, out_path = sys.argv[1], sys.argv[2]

replacements = {
    "__VERSION__": os.environ["VERSION"],
    "__URL__": os.environ["URL"],
    "__SHA256__": os.environ["SHA256"],
}

with open(template_path) as f:
    rendered = f.read()

for token, value in replacements.items():
    rendered = rendered.replace(token, value)

# A leftover placeholder means an env var was missing or the template drifted;
# fail loudly rather than push a manifest Scoop cannot install from.
if "__" in rendered:
    sys.exit("error: unresolved placeholder ('__') remains in the rendered manifest")

# The manifest is pushed as-is; a syntactically broken one bricks the bucket for
# every user, so prove it parses before writing it.
try:
    json.loads(rendered)
except json.JSONDecodeError as exc:
    sys.exit(f"error: rendered manifest is not valid JSON: {exc}")

with open(out_path, "w") as f:
    f.write(rendered)
```

**Verify:** render locally against dummy env and confirm it parses:

```bash
VERSION=0.0.0 URL=https://example/lode_0.0.0_windows_amd64.zip SHA256=deadbeef \
  python3 .github/scoop/render-manifest.py .github/scoop/worklode.json.template /tmp/m.json
python3 -c "import json,sys; json.load(open('/tmp/m.json')); print('ok')"
```

- [ ] `.github/scoop/worklode.json.template` created
- [ ] `.github/scoop/render-manifest.py` created
- [ ] Local render produces valid JSON with no `__` left

---

## Task 2 — `_build-windows.yml` reusable workflow

**Create `.github/workflows/_build-windows.yml`:**

```yaml
name: build-windows

# Reusable workflow: cross-compiles the Windows lode.exe, zips it, and attaches
# the zip to the tag's GitHub Release. Called by release.yml (hand-pushed v*
# tag) and promote-prod.yml (after promotion pushes its own v* tag). Both
# callers feed the resulting sha256 into update-scoop-bucket.
#
# Pure-Go cross-compile on Ubuntu (CGO disabled) — no Windows runner needed.
# The Release object is created by the bottle pipeline; this workflow is a
# needs-dependent of build-bottles in both callers, so it only ever attaches an
# asset to a Release that already exists (no concurrent-create race).

on:
  workflow_call:
    inputs:
      tag:
        description: v* tag to build the Windows binary for (must already exist on the remote)
        required: true
        type: string
    outputs:
      amd64_sha256:
        value: ${{ jobs.build.outputs.amd64_sha256 }}

permissions:
  contents: write

jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    outputs:
      amd64_sha256: ${{ steps.build.outputs.sha256 }}
    steps:
      - name: Checkout worklode at the release tag
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ inputs.tag }}
          lfs: true

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod

      - name: Build and zip lode.exe
        id: build
        env:
          TAG: ${{ inputs.tag }}
        run: |
          set -euo pipefail
          # Homebrew derives `version` from the v-tagged archive URL (leading v
          # stripped); match that so `lode --version` reads the same on both
          # channels.
          VERSION="${TAG#v}"
          CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
            -ldflags "-s -w -X github.com/sunstoneinstitute/worklode/internal/cmd.version=${VERSION}" \
            -o lode.exe ./cmd/lode

          ZIP="lode_${VERSION}_windows_amd64.zip"
          zip -q "$ZIP" lode.exe
          sha256=$(sha256sum "$ZIP" | awk '{print $1}')
          {
            echo "zip=${ZIP}"
            echo "sha256=${sha256}"
          } >> "$GITHUB_OUTPUT"

      - name: Attach zip to GitHub Release
        uses: softprops/action-gh-release@3d0d9888cb7fd7b750713d6e236d1fcb99157228 # v3.0.2
        with:
          tag_name: ${{ inputs.tag }}
          files: ${{ steps.build.outputs.zip }}
          fail_on_unmatched_files: true
```

Notes:
- No `generate_release_notes` here — the bottle `release` job already sets it;
  attaching a second asset to the same tag just adds the file.
- `lfs: true` matches the other checkouts (embedded design-system assets are
  LFS-tracked; a non-LFS checkout would embed pointer files).

- [ ] `.github/workflows/_build-windows.yml` created

---

## Task 3 — `update-scoop-bucket` composite action

**Create `.github/actions/update-scoop-bucket/action.yml`** — modeled directly
on `.github/actions/update-homebrew-tap/action.yml` (same deploy-key rationale,
same hardened SSH: `ssh-keygen -y` validation of the key, GitHub-meta
`known_hosts`, `GIT_SSH_COMMAND` with `IdentitiesOnly`/`StrictHostKeyChecking`):

```yaml
name: Update Scoop bucket
description: >
  Point the worklode manifest in sunstoneinstitute/scoop-bucket at a v* tag of
  this repo's Windows release asset.

# Runs as a composite action inside the caller's job for the same reason
# update-homebrew-tap does: the deploy key lives in the `release` environment,
# and an environment secret arrives empty in a called (on: workflow_call)
# workflow. The caller declares `environment: release` and passes the resolved
# secret in as `deploy-key`.

inputs:
  tag:
    description: Git tag to publish (e.g. v0.1.3)
    required: true
  deploy-key:
    description: >
      Private half of a write-enabled deploy key on
      sunstoneinstitute/scoop-bucket. Owned by that repo rather than a person,
      so the credential outlives any individual's account.
    required: true
  amd64-sha:
    description: sha256 of the lode_<version>_windows_amd64.zip release asset
    required: true

runs:
  using: composite
  steps:
    - name: Render manifest
      shell: bash
      env:
        TAG: ${{ inputs.tag }}
        REPO: ${{ github.repository }}
        SHA256: ${{ inputs.amd64-sha }}
      run: |
        set -euo pipefail
        VERSION="${TAG#v}"
        URL="https://github.com/${REPO}/releases/download/${TAG}/lode_${VERSION}_windows_amd64.zip"
        VERSION="$VERSION" URL="$URL" SHA256="$SHA256" \
          python3 .github/scoop/render-manifest.py \
            .github/scoop/worklode.json.template "$RUNNER_TEMP/worklode.json"

    - name: Update manifest in the bucket
      shell: bash
      env:
        SCOOP_DEPLOY_KEY: ${{ inputs.deploy-key }}
        TAG: ${{ inputs.tag }}
      run: |
        set -euo pipefail

        KEY_FILE="$RUNNER_TEMP/scoop_deploy_key"
        KNOWN_HOSTS="$RUNNER_TEMP/known_hosts"
        install -m 600 /dev/null "$KEY_FILE"
        printf '%s\n' "$SCOOP_DEPLOY_KEY" > "$KEY_FILE"

        # An empty/malformed secret otherwise surfaces as ssh's opaque
        # "error in libcrypto" + publickey denial, which reads like a missing
        # deploy key rather than a bad secret. The length is safe to print.
        if ! ssh-keygen -y -f "$KEY_FILE" > /dev/null 2>&1 < /dev/null; then
          echo "::error::SCOOP_DEPLOY_KEY is not a usable OpenSSH private key" \
               "— ${#SCOOP_DEPLOY_KEY} bytes received. Check that the calling job" \
               "declares 'environment: release'."
          exit 1
        fi

        curl -fsS https://api.github.com/meta \
          | jq -r '.ssh_keys[] | "github.com " + .' > "$KNOWN_HOSTS"

        export GIT_SSH_COMMAND="ssh -i $KEY_FILE -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KNOWN_HOSTS"

        git clone --depth 1 \
          git@github.com:sunstoneinstitute/scoop-bucket.git \
          "$RUNNER_TEMP/bucket"
        cd "$RUNNER_TEMP/bucket"

        # bucket/ is Scoop's manifest subdirectory; mkdir -p covers a
        # freshly-created bucket repo that does not have it yet.
        mkdir -p bucket
        cp "$RUNNER_TEMP/worklode.json" bucket/worklode.json

        # status --porcelain (not `git diff --quiet`) so this also works on an
        # unborn default branch in a brand-new empty bucket repo, where the
        # manifest is untracked rather than a diff against HEAD.
        if [ -z "$(git status --porcelain)" ]; then
          echo "::notice::Manifest already matches ${TAG} — nothing to push."
          exit 0
        fi

        git config user.name "github-actions[bot]"
        git config user.email "github-actions[bot]@users.noreply.github.com"
        git add bucket/worklode.json
        git commit -m "Update worklode to ${TAG}"
        git push origin HEAD:main

    - name: Output summary
      shell: bash
      env:
        TAG: ${{ inputs.tag }}
        SHA256: ${{ inputs.amd64-sha }}
      run: |
        {
          echo "### Scoop bucket updated"
          echo ""
          echo "**Tag:** \`${TAG}\`"
          echo "**amd64 zip sha256:** \`${SHA256}\`"
          echo "**Manifest:** sunstoneinstitute/scoop-bucket \`bucket/worklode.json\`"
          echo "**Install:** \`scoop bucket add sunstone https://github.com/sunstoneinstitute/scoop-bucket && scoop install sunstone/worklode\`"
        } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] `.github/actions/update-scoop-bucket/action.yml` created

---

## Task 4 — Wire into `release.yml`

Add two jobs after the existing `homebrew` job. `build-windows` depends on
`build-bottles` so the Release object exists before it attaches the zip; `scoop`
consumes the sha and pushes the manifest.

```yaml
  build-windows:
    needs: build-bottles
    uses: ./.github/workflows/_build-windows.yml
    with:
      tag: ${{ inputs.tag || github.ref_name }}

  scoop:
    needs: build-windows
    runs-on: ubuntu-latest
    # Same reasoning as the homebrew job: environment secret must be resolved
    # in a job that declares `environment: release`, not across a reusable
    # workflow boundary.
    environment: release
    timeout-minutes: 10
    steps:
      - name: Checkout code
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          lfs: true

      - name: Update Scoop bucket
        uses: ./.github/actions/update-scoop-bucket
        with:
          tag: ${{ inputs.tag || github.ref_name }}
          deploy-key: ${{ secrets.SCOOP_DEPLOY_KEY }}
          amd64-sha: ${{ needs.build-windows.outputs.amd64_sha256 }}
```

- [ ] `release.yml` has `build-windows` + `scoop` jobs
- [ ] `actionlint`/`yamllint` (or `gh workflow view` after push) shows no syntax error

---

## Task 5 — Wire into `promote-prod.yml`

Same two jobs, keyed off `needs.promote.outputs.tag` (so both jobs list
`promote` in `needs`).

```yaml
  build-windows:
    needs: [promote, build-bottles]
    uses: ./.github/workflows/_build-windows.yml
    with:
      tag: ${{ needs.promote.outputs.tag }}

  scoop:
    needs: [promote, build-windows]
    runs-on: ubuntu-latest
    environment: release
    timeout-minutes: 10
    steps:
      - name: Checkout code
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          lfs: true

      - name: Update Scoop bucket
        uses: ./.github/actions/update-scoop-bucket
        with:
          tag: ${{ needs.promote.outputs.tag }}
          deploy-key: ${{ secrets.SCOOP_DEPLOY_KEY }}
          amd64-sha: ${{ needs.build-windows.outputs.amd64_sha256 }}
```

- [ ] `promote-prod.yml` has `build-windows` + `scoop` jobs

---

## Task 6 — Deploy-key setup wizard

Using **mattpocock-skills:wizard**, generate
`scripts/setup-scoop-deploy-key.sh` — an interactive bash wizard that performs
the one-time prerequisite above:

1. `ssh-keygen -t ed25519 -N "" -C "worklode-scoop-deploy" -f ./scoop_deploy_key`
2. Print the public key and open/echo
   `https://github.com/sunstoneinstitute/scoop-bucket/settings/keys`, instruct
   the operator to add it as a deploy key **with write access**, and pause for
   confirmation.
3. `gh secret set SCOOP_DEPLOY_KEY --env release -R sunstoneinstitute/worklode < ./scoop_deploy_key`
4. `rm -f ./scoop_deploy_key ./scoop_deploy_key.pub` and confirm.

Guard the destructive/irreversible steps (secret set) behind an explicit
confirmation, and check `gh auth status` up front.

- [ ] `scripts/setup-scoop-deploy-key.sh` created, executable, guarded

---

## Task 7 — Document the Windows install path

In `README.md` Quickstart, extend the "Install the `lode` CLI" block (currently
`go install ./cmd/lode`) with the package-manager options, so macOS and Windows
users see their native path:

```markdown
Or via a package manager:

- macOS (Homebrew): `brew install sunstoneinstitute/tap/worklode`
- Windows (Scoop): `scoop bucket add sunstone https://github.com/sunstoneinstitute/scoop-bucket && scoop install sunstone/worklode`
```

- [ ] README Quickstart documents Homebrew + Scoop install

---

## Verification

- **Render script:** local render (Task 1) yields valid JSON, no `__` left.
- **Workflow syntax:** `actionlint` clean on `_build-windows.yml`, `release.yml`,
  `promote-prod.yml` (or confirm via `gh workflow view` after the branch lands).
- **First real release (after the deploy key exists):** tag a `v*`, confirm the
  Release gains `lode_<version>_windows_amd64.zip`, and
  `sunstoneinstitute/scoop-bucket` gains/updates `bucket/worklode.json` pointing
  at that asset with a matching hash.
- **End-to-end (optional, on a Windows box):**
  `scoop bucket add sunstone https://github.com/sunstoneinstitute/scoop-bucket`
  then `scoop install sunstone/worklode` and `lode --version` reports the tag.
