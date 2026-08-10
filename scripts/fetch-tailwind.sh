#!/usr/bin/env bash
# Fetch the pinned Tailwind CSS v4 standalone CLI into bin/, verifying sha256.
# Idempotent: re-running with the binary already present and correct is a no-op.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/tailwind.sha256   # sets TAILWIND_VERSION and SHA256_<platform>

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)  plat=macos-arm64;  sum="$SHA256_macos_arm64" ;;
  Darwin-x86_64) plat=macos-x64;    sum="$SHA256_macos_x64" ;;
  Linux-x86_64)  plat=linux-x64;    sum="$SHA256_linux_x64" ;;
  Linux-aarch64) plat=linux-arm64;  sum="$SHA256_linux_arm64" ;;
  *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

mkdir -p bin
if [ -x bin/tailwindcss ] && echo "$sum  bin/tailwindcss" | shasum -a 256 -c - >/dev/null 2>&1; then
  exit 0
fi
url="https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-${plat}"
curl -fsSL "$url" -o bin/tailwindcss
echo "$sum  bin/tailwindcss" | shasum -a 256 -c -
chmod +x bin/tailwindcss
