---
status: superseded
---
# Worklode Homebrew Bottles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the `worklode` Homebrew formula real bottles (arm64_sequoia + arm64_tahoe) built and published automatically on every release, matching the pattern already proven in `sunstoneinstitute/horndb`.

**Architecture:** A new reusable workflow (`_build-bottles.yml`) runs on two macOS runners (macos-15 → `arm64_sequoia`, macos-26 → `arm64_tahoe`). Each leg taps the real `sunstoneinstitute/tap`, overwrites the formula there with a bottle-free render of a shared template, then runs the **real** `brew install --build-bottle` + `brew bottle` — not a hand-rolled tarball — because worklode's `install()` calls Homebrew helpers (`std_go_args`, `generate_completions_from_executable`) that are impractical to reimplement correctly outside brew. A `release` job downloads both bottle artifacts, renames each to the filename Homebrew's pour logic actually requests (`name-version.tag.bottle.tar.gz`, single dash — *not* the `local_filename` brew writes to disk, which uses a double dash), and attaches them to a GitHub Release for the tag. `update-homebrew-tap` (the composite action both `release.yml` and `promote-prod.yml` already call) is extended to render the same shared template *with* a `bottle do` block and push the result to the tap.

This whole approach — template + render script, JSON field names, and the single-vs-double-dash filename gotcha — was validated end-to-end on this machine before writing this plan: built a real bottle for the current v0.3.0 formula, served it from a local `file://` root_url, and confirmed `brew install` poured it (not built from source) and the resulting `lode --version` worked. Do not re-derive this from scratch; the steps below encode exactly what worked.

**Tech Stack:** GitHub Actions (macOS + Ubuntu runners), Homebrew (`brew install --build-bottle`, `brew bottle --json`), Python 3 (template rendering), `jq`.

---

## Key facts from local validation (do not re-verify, just use)

- `brew bottle --json` writes a `<name>--<version>.<tag>.bottle.json` file. Its top-level key is the tap-qualified formula name (e.g. `"sunstoneinstitute/tap/worklode"`) — read it via `jq -r '.[keys[0]]. ...'` so the exact key doesn't need to be hardcoded.
- `.bottle.cellar` is a **single value for the whole bottle object**, not per-tag (e.g. `"any_skip_relocation"` for this Go binary — confirmed locally, do not hardcode `:any`, use whatever `brew bottle` reports).
- `.bottle.tags.<tag>.local_filename` (e.g. `worklode--0.3.0.arm64_tahoe.bottle.tar.gz`, **double dash**) is the file `brew bottle` actually writes to disk.
- `.bottle.tags.<tag>.filename` (e.g. `worklode-0.3.0.arm64_tahoe.bottle.tar.gz`, **single dash**) is the filename Homebrew's pour logic requests from `root_url` at install time. **The file uploaded to the GitHub Release must be renamed from `local_filename` to `filename` before upload**, or `brew install` fails with `curl: (37) Couldn't open file ...` (reproduced locally; renaming fixed it).
- `.bottle.tags.<tag>.sha256` is the sha256 of the bottle tarball — matches a plain `shasum -a 256` of the (renamed) file, so the release job can just re-hash the downloaded artifact rather than trust a value passed out-of-band.
- `brew style` and `brew audit --formula` both pass clean on the final formula only when `head` comes **before** `bottle do...end` (confirmed — `brew style` flags `head should be put before bottle` otherwise).
- The formula must live inside a tap for `brew install`/`brew style`/`brew audit` to accept it at all (`brew install --build-bottle ./worklode.rb` from a bare directory fails with "Homebrew requires formulae to be in a tap").

---

## File Structure

- Create: `worklode/.github/homebrew/worklode.rb.template` — single source of truth for the formula body (desc/homepage/head/depends_on/install/test), with `__URL__`/`__SHA256__`/`__BOTTLE_BLOCK__` placeholders.
- Create: `worklode/.github/homebrew/render-formula.py` — fills the template from env vars. Renders the bottle-free build formula (bottle env vars unset) or the full publish formula (bottle env vars set) depending on which env vars are present. Used by both the bottling job and the tap-push action, so the exact same install() logic is what gets built into the bottle and what's advertised as being built into the bottle.
- Create: `worklode/.github/workflows/_build-bottles.yml` — reusable workflow (`workflow_call`): `bottle` matrix job (macos-15/macos-26) + `release` job that publishes to the GitHub Release and exposes the sha256/cellar as workflow outputs.
- Modify: `worklode/.github/actions/update-homebrew-tap/action.yml` — accepts the new bottle inputs, renders the full formula via the shared template/script, and replaces the old sed-based patch with a full-file write + commit.
- Modify: `worklode/.github/workflows/release.yml` — bump `permissions.contents` to `write`, add a `build-bottles` job, feed its outputs into `homebrew`.
- Modify: `worklode/.github/workflows/promote-prod.yml` — same wiring, using `needs.promote.outputs.tag`.

No files in `homebrew-tap` need manual edits — `Formula/worklode.rb` gets regenerated by the updated action the next time it runs (including via a manual `workflow_dispatch` for the existing `v0.3.0` tag — see the final task).

---

### Task 1: Formula template and render script

**Files:**
- Create: `.github/homebrew/worklode.rb.template`
- Create: `.github/homebrew/render-formula.py`

- [ ] **Step 1: Write the template**

```
class Worklode < Formula
  desc "Work tracker CLI (lode) for Sunstone Institute"
  homepage "https://github.com/sunstoneinstitute/worklode"
  url "__URL__"
  sha256 "__SHA256__"
  head "https://github.com/sunstoneinstitute/worklode.git", branch: "main"
__BOTTLE_BLOCK__
  depends_on "go" => :build

  def install
    ldflags = %W[
      -s -w
      -X github.com/sunstoneinstitute/worklode/internal/cmd.version=#{version}
    ]
    system "go", "build", *std_go_args(ldflags:, output: bin/"lode"), "./cmd/lode"

    generate_completions_from_executable(bin/"lode", "completion")
  end

  test do
    assert_match "lode version", shell_output("#{bin}/lode --version")
  end
end
```

Save this exact content (including the trailing newline after `end`) to `.github/homebrew/worklode.rb.template`.

- [ ] **Step 2: Write the render script**

```python
import os, sys


def build_bottle_block():
    root_url = os.environ.get("ROOT_URL")
    sequoia = os.environ.get("ARM64_SEQUOIA_SHA")
    tahoe = os.environ.get("ARM64_TAHOE_SHA")
    cellar = os.environ.get("CELLAR")
    if not (root_url and sequoia and tahoe and cellar):
        return ""
    return (
        "\n"
        "  # Bottles are poured by arch; brew falls back to an older-OS bottle of\n"
        "  # the same arch on newer macOS, and to a source build if none match.\n"
        "  bottle do\n"
        f'    root_url "{root_url}"\n'
        f'    sha256 cellar: :{cellar}, arm64_sequoia: "{sequoia}"\n'
        f'    sha256 cellar: :{cellar}, arm64_tahoe:   "{tahoe}"\n'
        "  end\n"
    )


template_path, out_path = sys.argv[1], sys.argv[2]
url = os.environ["URL"]
sha256 = os.environ["SHA256"]
bottle_block = build_bottle_block()

with open(template_path) as f:
    lines = f.readlines()

out = []
for line in lines:
    if line.strip() == "__BOTTLE_BLOCK__":
        if bottle_block:
            out.append(bottle_block)
        continue
    out.append(line.replace("__URL__", url).replace("__SHA256__", sha256))

with open(out_path, "w") as f:
    f.writelines(out)
```

Save to `.github/homebrew/render-formula.py`.

- [ ] **Step 3: Verify both render modes locally**

Run:
```bash
cd /tmp && mkdir -p render-check && cd render-check
URL="https://example.com/x.tar.gz" SHA256="$(printf 'a%.0s' {1..64})" \
  python3 /path/to/worklode/.github/homebrew/render-formula.py \
  /path/to/worklode/.github/homebrew/worklode.rb.template build.rb

URL="https://example.com/x.tar.gz" SHA256="$(printf 'a%.0s' {1..64})" \
  ROOT_URL="https://example.com/rel" \
  ARM64_SEQUOIA_SHA="$(printf 'b%.0s' {1..64})" \
  ARM64_TAHOE_SHA="$(printf 'c%.0s' {1..64})" \
  CELLAR="any_skip_relocation" \
  python3 /path/to/worklode/.github/homebrew/render-formula.py \
  /path/to/worklode/.github/homebrew/worklode.rb.template publish.rb

diff build.rb publish.rb
```
Expected: `diff` shows `publish.rb` has the extra `bottle do...end` block (with `head` still before it) and nothing else differs. Both files should be syntactically plausible Ruby (eyeball it — no unresolved `__..__` tokens).

- [ ] **Step 4: Commit**

```bash
git add .github/homebrew/worklode.rb.template .github/homebrew/render-formula.py
git commit -m "Add shared Homebrew formula template and renderer"
```

---

### Task 2: `_build-bottles.yml` reusable workflow

**Files:**
- Create: `.github/workflows/_build-bottles.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: build-bottles

# Reusable workflow: builds real Homebrew bottles for worklode on both
# supported macOS generations and attaches them to the tag's GitHub Release.
# Called by release.yml (hand-pushed v* tag) and promote-prod.yml (after
# promoting to prod pushes its own v* tag). Both callers feed the resulting
# sha256s into update-homebrew-tap.
#
# Bottling runs the real `brew install --build-bottle` + `brew bottle`
# against the formula's actual install() logic (which calls Homebrew
# helpers like generate_completions_from_executable) rather than hand-
# packaging a tarball — hand-replicating that logic risks shipping a bottle
# that doesn't match what `brew install` from source actually produces.

on:
  workflow_call:
    inputs:
      tag:
        description: v* tag to build bottles for (must already exist on the remote)
        required: true
        type: string
    outputs:
      arm64_sequoia_sha256:
        value: ${{ jobs.release.outputs.arm64_sequoia_sha256 }}
      arm64_tahoe_sha256:
        value: ${{ jobs.release.outputs.arm64_tahoe_sha256 }}
      cellar:
        value: ${{ jobs.release.outputs.cellar }}

permissions:
  contents: write

jobs:
  bottle:
    name: bottle (${{ matrix.bottle_tag }})
    strategy:
      fail-fast: false
      matrix:
        include:
          - os: macos-15 # Apple Silicon, macOS Sequoia
            bottle_tag: arm64_sequoia
          - os: macos-26 # Apple Silicon, macOS Tahoe
            bottle_tag: arm64_tahoe
    runs-on: ${{ matrix.os }}
    steps:
      - name: Checkout worklode at the release tag
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
        with:
          ref: ${{ inputs.tag }}

      - name: Compute source tarball sha256
        id: tarball
        env:
          TAG: ${{ inputs.tag }}
        run: |
          set -euo pipefail
          URL="https://github.com/${GITHUB_REPOSITORY}/archive/refs/tags/${TAG}.tar.gz"
          curl -fsSL --retry 5 --retry-delay 5 --retry-all-errors -o "$RUNNER_TEMP/src.tgz" "$URL"
          {
            echo "url=${URL}"
            echo "sha256=$(shasum -a 256 "$RUNNER_TEMP/src.tgz" | awk '{print $1}')"
          } >> "$GITHUB_OUTPUT"

      - name: Tap sunstoneinstitute/tap
        run: brew tap sunstoneinstitute/tap

      - name: Render a bottle-free formula for building
        env:
          URL: ${{ steps.tarball.outputs.url }}
          SHA256: ${{ steps.tarball.outputs.sha256 }}
        run: |
          set -euo pipefail
          TAP_FORMULA="$(brew --repo sunstoneinstitute/tap)/Formula/worklode.rb"
          python3 .github/homebrew/render-formula.py \
            .github/homebrew/worklode.rb.template "$TAP_FORMULA"

      - name: Build and bottle
        id: bottle
        env:
          HOMEBREW_NO_AUTO_UPDATE: "1"
          HOMEBREW_NO_INSTALL_CLEANUP: "1"
        run: |
          set -euo pipefail
          brew install --build-bottle --verbose sunstoneinstitute/tap/worklode

          mkdir -p "$RUNNER_TEMP/bottle"
          cd "$RUNNER_TEMP/bottle"
          brew bottle --json --no-rebuild sunstoneinstitute/tap/worklode

          json=$(ls worklode--*.bottle.json)
          tag='${{ matrix.bottle_tag }}'
          cellar=$(jq -r --arg tag "$tag" '.[keys[0]].bottle.cellar' "$json")
          sha256=$(jq -r --arg tag "$tag" '.[keys[0]].bottle.tags[$tag].sha256' "$json")
          local_filename=$(jq -r --arg tag "$tag" '.[keys[0]].bottle.tags[$tag].local_filename' "$json")
          filename=$(jq -r --arg tag "$tag" '.[keys[0]].bottle.tags[$tag].filename' "$json")

          # brew bottle writes `local_filename` (double dash) to disk, but
          # brew's pour logic requests `filename` (single dash) from
          # root_url at install time — confirmed locally, renaming here is
          # required or `brew install` 404s trying to fetch the bottle.
          mv "$local_filename" "$filename"
          echo -n "$cellar" > "${filename}.cellar"

          {
            echo "file=${filename}"
            echo "sha256=${sha256}"
            echo "cellar=${cellar}"
          } >> "$GITHUB_OUTPUT"

      - name: Upload bottle artifact
        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: bottle-${{ matrix.bottle_tag }}
          path: |
            ${{ runner.temp }}/bottle/${{ steps.bottle.outputs.file }}
            ${{ runner.temp }}/bottle/${{ steps.bottle.outputs.file }}.cellar
          if-no-files-found: error

  release:
    name: Publish bottles to GitHub Release
    needs: bottle
    runs-on: ubuntu-latest
    outputs:
      arm64_sequoia_sha256: ${{ steps.shas.outputs.arm64_sequoia }}
      arm64_tahoe_sha256: ${{ steps.shas.outputs.arm64_tahoe }}
      cellar: ${{ steps.shas.outputs.cellar }}
    steps:
      - name: Download bottle artifacts
        uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          pattern: bottle-*
          path: dist
          merge-multiple: true

      - name: Compute shas and verify cellar agreement
        id: shas
        run: |
          set -euo pipefail
          sequoia_file=$(ls dist/*.arm64_sequoia.bottle.tar.gz)
          tahoe_file=$(ls dist/*.arm64_tahoe.bottle.tar.gz)
          sequoia_sha=$(shasum -a 256 "$sequoia_file" | awk '{print $1}')
          tahoe_sha=$(shasum -a 256 "$tahoe_file" | awk '{print $1}')

          # Both legs build the same pure-Go binary, so brew should report
          # the same cellar spec for each; a mismatch means something about
          # the build diverged between runners and needs investigating
          # rather than silently picking one value.
          sequoia_cellar=$(cat "${sequoia_file}.cellar")
          tahoe_cellar=$(cat "${tahoe_file}.cellar")
          if [ "$sequoia_cellar" != "$tahoe_cellar" ]; then
            echo "::error::cellar spec differs between arm64_sequoia ($sequoia_cellar) and arm64_tahoe ($tahoe_cellar) — investigate before publishing a formula that can only state one."
            exit 1
          fi

          {
            echo "arm64_sequoia=${sequoia_sha}"
            echo "arm64_tahoe=${tahoe_sha}"
            echo "cellar=${sequoia_cellar}"
          } >> "$GITHUB_OUTPUT"

      - name: Publish/attach to GitHub Release
        uses: softprops/action-gh-release@3d0d9888cb7fd7b750713d6e236d1fcb99157228 # v3.0.2
        with:
          tag_name: ${{ inputs.tag }}
          files: dist/*.bottle.tar.gz
          fail_on_unmatched_files: true
          generate_release_notes: true
```

- [ ] **Step 2: Lint the workflow**

Run: `actionlint .github/workflows/_build-bottles.yml`
Expected: no output (clean). Fix any reported issues (e.g. quoting, unknown context) before moving on — do not proceed with a workflow actionlint flags.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/_build-bottles.yml
git commit -m "Add reusable workflow to build and publish worklode bottles"
```

---

### Task 3: Extend `update-homebrew-tap` to push the bottle block

**Files:**
- Modify: `.github/actions/update-homebrew-tap/action.yml`

- [ ] **Step 1: Read the current file to confirm line numbers before editing**

Run: `cat -n .github/actions/update-homebrew-tap/action.yml`

- [ ] **Step 2: Add three new inputs**

After the existing `deploy-key` input block (currently ends around line 28), add:

```yaml
  arm64-sequoia-sha:
    description: sha256 of the arm64_sequoia bottle tarball
    required: true
  arm64-tahoe-sha:
    description: sha256 of the arm64_tahoe bottle tarball
    required: true
  cellar:
    description: >
      Bottle cellar spec as reported by `brew bottle` (e.g.
      any_skip_relocation), identical for both bottles since they're the
      same pure-Go binary.
    required: true
```

- [ ] **Step 3: Replace the "Update formula in the tap" step**

Replace the entire step (currently the `sed`-based one, roughly lines 50-108) with:

```yaml
    - name: Render final formula
      id: render
      shell: bash
      env:
        URL: ${{ steps.tarball.outputs.url }}
        SHA256: ${{ steps.tarball.outputs.sha256 }}
        ROOT_URL: https://github.com/${{ github.repository }}/releases/download/${{ inputs.tag }}
        ARM64_SEQUOIA_SHA: ${{ inputs.arm64-sequoia-sha }}
        ARM64_TAHOE_SHA: ${{ inputs.arm64-tahoe-sha }}
        CELLAR: ${{ inputs.cellar }}
      run: |
        set -euo pipefail
        python3 .github/homebrew/render-formula.py \
          .github/homebrew/worklode.rb.template "$RUNNER_TEMP/worklode.rb"

    - name: Update formula in the tap
      shell: bash
      env:
        TAP_DEPLOY_KEY: ${{ inputs.deploy-key }}
        TAG: ${{ inputs.tag }}
      run: |
        set -euo pipefail

        KEY_FILE="$RUNNER_TEMP/tap_deploy_key"
        KNOWN_HOSTS="$RUNNER_TEMP/known_hosts"
        install -m 600 /dev/null "$KEY_FILE"
        printf '%s\n' "$TAP_DEPLOY_KEY" > "$KEY_FILE"

        # An empty or malformed secret otherwise surfaces as ssh's opaque
        # "error in libcrypto" followed by a publickey denial, which reads
        # like a missing deploy key rather than a bad secret. The length is
        # safe to print: a wrong length is the diagnosis, not the key.
        if ! ssh-keygen -y -f "$KEY_FILE" > /dev/null 2>&1 < /dev/null; then
          echo "::error::TAP_DEPLOY_KEY is not a usable OpenSSH private key" \
               "— ${#TAP_DEPLOY_KEY} bytes received. Check that the calling job" \
               "declares 'environment: release'."
          exit 1
        fi

        # Host keys from the GitHub meta API over TLS, rather than a
        # trust-on-first-use ssh-keyscan or a hardcoded key that rots when
        # GitHub rotates theirs.
        curl -fsS https://api.github.com/meta \
          | jq -r '.ssh_keys[] | "github.com " + .' > "$KNOWN_HOSTS"

        export GIT_SSH_COMMAND="ssh -i $KEY_FILE -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KNOWN_HOSTS"

        git clone --depth 1 \
          git@github.com:sunstoneinstitute/homebrew-tap.git \
          "$RUNNER_TEMP/tap"
        cd "$RUNNER_TEMP/tap"

        cp "$RUNNER_TEMP/worklode.rb" Formula/worklode.rb

        if git diff --quiet; then
          echo "::notice::Formula already matches ${TAG} — nothing to push."
          exit 0
        fi

        git config user.name "github-actions[bot]"
        git config user.email "github-actions[bot]@users.noreply.github.com"
        git commit -am "Update worklode to ${TAG}"
        git push
```

Keep the existing "Compute source tarball sha256" step (`id: tarball`) unchanged — the new render step consumes its outputs the same way the old sed step did. Keep the existing "Output summary" step, but extend it:

```yaml
    - name: Output summary
      shell: bash
      env:
        TAG: ${{ inputs.tag }}
        SHA256: ${{ steps.tarball.outputs.sha256 }}
        ARM64_SEQUOIA_SHA: ${{ inputs.arm64-sequoia-sha }}
        ARM64_TAHOE_SHA: ${{ inputs.arm64-tahoe-sha }}
      run: |
        {
          echo "### Homebrew tap updated"
          echo ""
          echo "**Tag:** \`${TAG}\`"
          echo "**Tarball sha256:** \`${SHA256}\`"
          echo "**arm64_sequoia bottle sha256:** \`${ARM64_SEQUOIA_SHA}\`"
          echo "**arm64_tahoe bottle sha256:** \`${ARM64_TAHOE_SHA}\`"
          echo "**Formula:** sunstoneinstitute/homebrew-tap \`Formula/worklode.rb\`"
        } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 4: Lint**

Run: `actionlint .github/actions/update-homebrew-tap/action.yml`
Expected: no output.

- [ ] **Step 5: Dry-run the render step locally with fabricated inputs**

```bash
cd /path/to/worklode
URL="https://github.com/sunstoneinstitute/worklode/archive/refs/tags/v0.3.0.tar.gz" \
SHA256="e6a97175aecbee41587adb305f36102281df7d3eba8388cac73d391c7fca29fd" \
ROOT_URL="https://github.com/sunstoneinstitute/worklode/releases/download/v0.3.0" \
ARM64_SEQUOIA_SHA="$(printf 'a%.0s' {1..64})" \
ARM64_TAHOE_SHA="$(printf 'b%.0s' {1..64})" \
CELLAR="any_skip_relocation" \
python3 .github/homebrew/render-formula.py \
  .github/homebrew/worklode.rb.template /tmp/worklode-rendered.rb
cat /tmp/worklode-rendered.rb
```
Expected: valid-looking formula with `head` before `bottle do`, both sha256 lines populated, `root_url` set. (This exact shape was already confirmed clean under `brew style` during plan research — no need to re-run `brew style` here, just eyeball that the substitution didn't leave any `__TOKEN__` behind.)

- [ ] **Step 6: Commit**

```bash
git add .github/actions/update-homebrew-tap/action.yml
git commit -m "Push a bottle block from update-homebrew-tap"
```

---

### Task 4: Wire bottling into `release.yml`

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Bump permissions**

Change:
```yaml
permissions:
  contents: read
```
to:
```yaml
permissions:
  contents: write
```
A called reusable workflow cannot request more than the caller grants; `_build-bottles.yml` needs `contents: write` to create the GitHub Release.

- [ ] **Step 2: Add the `build-bottles` job and wire `homebrew` to depend on it**

Replace the `jobs:` block with:

```yaml
jobs:
  build-bottles:
    uses: ./.github/workflows/_build-bottles.yml
    with:
      tag: ${{ inputs.tag || github.ref_name }}

  homebrew:
    needs: build-bottles
    runs-on: ubuntu-latest
    # Resolves TAP_DEPLOY_KEY and gates it to the refs the environment's branch
    # policy allows. See the same job in promote-prod.yml for why the
    # environment sits here rather than behind a reusable workflow.
    environment: release
    timeout-minutes: 10
    steps:
      # The tap update is a local composite action, so the repo must be on disk
      # before it can be resolved.
      - name: Checkout code
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0

      - name: Update Homebrew tap
        uses: ./.github/actions/update-homebrew-tap
        with:
          tag: ${{ inputs.tag || github.ref_name }}
          deploy-key: ${{ secrets.TAP_DEPLOY_KEY }}
          arm64-sequoia-sha: ${{ needs.build-bottles.outputs.arm64_sequoia_sha256 }}
          arm64-tahoe-sha: ${{ needs.build-bottles.outputs.arm64_tahoe_sha256 }}
          cellar: ${{ needs.build-bottles.outputs.cellar }}
```

- [ ] **Step 3: Lint**

Run: `actionlint .github/workflows/release.yml`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "Build and publish bottles before updating the tap on release"
```

---

### Task 5: Wire bottling into `promote-prod.yml`

**Files:**
- Modify: `.github/workflows/promote-prod.yml`

- [ ] **Step 1: Add the `build-bottles` job**

Insert a new job (permissions are already `contents: write` at the workflow level, no change needed there):

```yaml
  build-bottles:
    needs: promote
    uses: ./.github/workflows/_build-bottles.yml
    with:
      tag: ${{ needs.promote.outputs.tag }}
```

- [ ] **Step 2: Update the `homebrew` job**

Change:
```yaml
  homebrew:
    needs: promote
```
to:
```yaml
  homebrew:
    needs: [promote, build-bottles]
```

And extend its `Update Homebrew tap` step's `with:` block:

```yaml
      - name: Update Homebrew tap
        uses: ./.github/actions/update-homebrew-tap
        with:
          tag: ${{ needs.promote.outputs.tag }}
          deploy-key: ${{ secrets.TAP_DEPLOY_KEY }}
          arm64-sequoia-sha: ${{ needs.build-bottles.outputs.arm64_sequoia_sha256 }}
          arm64-tahoe-sha: ${{ needs.build-bottles.outputs.arm64_tahoe_sha256 }}
          cellar: ${{ needs.build-bottles.outputs.cellar }}
```

- [ ] **Step 3: Lint**

Run: `actionlint .github/workflows/promote-prod.yml`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/promote-prod.yml
git commit -m "Build and publish bottles before updating the tap on promote"
```

---

### Task 6: Full-repo lint pass

**Files:** none (verification only)

- [ ] **Step 1: Lint every workflow and action file in the repo**

Run: `actionlint`
Expected: no output. This catches cross-file issues (e.g. a job referencing an output that doesn't exist) that per-file linting in earlier tasks might miss.

- [ ] **Step 2: gofmt/vet sanity check (nothing Go changed, but confirm the repo is still clean)**

Run: `gofmt -l . && go vet ./...`
Expected: `gofmt -l .` prints nothing, `go vet` exits 0.

- [ ] **Step 3: Review the full diff**

Run: `git diff main --stat` (or `git log --oneline main..HEAD` plus `git diff main`)
Expected: only the 5 files from Tasks 1-5 changed (`worklode.rb.template`, `render-formula.py`, `_build-bottles.yml`, `update-homebrew-tap/action.yml`, `release.yml`, `promote-prod.yml` — 6 files). No unrelated changes.

---

## After the plan: retroactively bottle v0.3.0

This is **not** a subagent task — it triggers real CI that pushes to the shared `sunstoneinstitute/homebrew-tap` repo, so get explicit go-ahead before running it, after the branch above is merged to `main`:

```bash
gh workflow run release.yml --repo sunstoneinstitute/worklode -f tag=v0.3.0
```

Then watch it with `gh run watch --repo sunstoneinstitute/worklode` (or `gh run list` to find the run ID first). Once it succeeds, verify from a clean state:

```bash
brew uninstall worklode
brew untap sunstoneinstitute/tap
brew install sunstoneinstitute/tap/worklode --verbose
```

Expected output includes `Pouring worklode-0.3.0.arm64_tahoe.bottle.tar.gz` (not a `go build` line) — confirming the bottle path is actually used, not a silent fallback to source.
