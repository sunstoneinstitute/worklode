#!/usr/bin/env bash
# Regenerate the cockpit's committed artifacts: templ Go and the Tailwind CSS.
set -euo pipefail
cd "$(dirname "$0")/.."
go tool templ generate ./internal/ui/...
./bin/tailwindcss -i internal/ui/styles/app.tailwind.css -o internal/ui/assets/app.css
