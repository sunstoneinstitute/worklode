#!/usr/bin/env bash
# Build cmd/lode and install it to /usr/local/bin, then log in to the
# Sunstone worklode instance.
set -euo pipefail
cd "$(dirname "$0")"

go build -o /usr/local/bin/lode ./cmd/lode

lode login --server https://worklode.dev.sunstoneinstitute.ai
