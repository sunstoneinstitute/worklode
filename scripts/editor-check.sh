#!/usr/bin/env bash
# Run the WL-855 BlockNote spike's browser check.
#
# Renders the document page with the editor island on (?editor=1), serves it
# with the cockpit's real Content-Security-Policy and the real embedded assets,
# and in a headless browser reports whether the editor mounts under that policy
# and what a real document body loses on the markdown round trip.
#
# It needs no Postgres and no running server, but it does need a Chrome-family
# browser, which CI deliberately does not install (spec 032 §12). With none on
# the machine it prints how to point at one and exits 0.
#
# Usage: editor-check.sh [extra go test flags]
#   LODE_NARROW_BROWSER=/path/to/chrome        use this browser instead of searching
#   LODE_EDITOR_ROUNDTRIP_OUT=/path/to/out.md  also write the exported markdown there

set -euo pipefail
cd "$(dirname "$0")/.."

exec go test -trimpath -count=1 -tags narrowcheck -v \
  -run TestDocEditorIslandUnderCSP ./internal/ui/ "$@"
