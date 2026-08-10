#!/usr/bin/env bash
# Regenerate the cockpit's committed artifacts: templ Go and the Tailwind CSS.
set -euo pipefail
cd "$(dirname "$0")/.."
go tool templ generate ./internal/api/...
./bin/tailwindcss -i internal/api/assets/app.tailwind.css -o internal/api/assets/app.css
