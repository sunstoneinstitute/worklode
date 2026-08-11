#!/usr/bin/env bash
# One-time setup: mint an ed25519 deploy key for sunstoneinstitute/scoop-bucket
# and store its private half as the SCOOP_DEPLOY_KEY secret in this repo's
# `release` GitHub environment. Mirrors the existing TAP_DEPLOY_KEY setup.
#
# Run by an operator, once, before the first Scoop release. Safe to re-run —
# each run mints a fresh keypair and overwrites the secret.
set -euo pipefail

BUCKET_KEYS_URL="https://github.com/sunstoneinstitute/scoop-bucket/settings/keys/new"
REPO="sunstoneinstitute/worklode"

echo "==> [1/5] preflight checks" >&2
command -v ssh-keygen >/dev/null 2>&1 || {
	echo "setup-scoop-deploy-key: ssh-keygen not found — install OpenSSH" >&2
	exit 1
}
command -v gh >/dev/null 2>&1 || {
	echo "setup-scoop-deploy-key: gh (GitHub CLI) not found" >&2
	exit 1
}
gh auth status >/dev/null 2>&1 || {
	echo "setup-scoop-deploy-key: gh is not authenticated — run 'gh auth login'" >&2
	exit 1
}

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
keyfile="$workdir/scoop_deploy_key"

echo "==> [2/5] generating ed25519 keypair" >&2
ssh-keygen -t ed25519 -N "" -C "worklode-scoop-deploy" -f "$keyfile" >/dev/null

echo "==> [3/5] add the public key as a write-enabled deploy key" >&2
echo "Public key:" >&2
cat "$keyfile.pub" >&2
echo >&2
echo "1. Open $BUCKET_KEYS_URL" >&2
echo "2. Paste the public key above." >&2
echo "3. Check 'Allow write access' — required, the release job pushes commits." >&2
echo "4. Click 'Add key'." >&2
read -n 1 -r -s -p "Press any key once the deploy key has been added... " >&2
echo >&2

echo "==> [4/5] set the SCOOP_DEPLOY_KEY secret" >&2
echo "This sets SCOOP_DEPLOY_KEY in the 'release' environment of $REPO" >&2
echo "from the private key just generated, overwriting any existing value." >&2
read -r -p "Type yes to proceed: " confirm >&2
if [ "$confirm" != "yes" ]; then
	echo "setup-scoop-deploy-key: aborted, secret not changed" >&2
	exit 1
fi
gh secret set SCOOP_DEPLOY_KEY --env release -R "$REPO" < "$keyfile"

echo "==> [5/5] done" >&2
echo "SCOOP_DEPLOY_KEY is set in the release environment of $REPO." >&2
echo "Local key files removed (trap cleanup on exit)." >&2
