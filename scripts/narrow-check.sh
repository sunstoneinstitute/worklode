#!/usr/bin/env bash
# Re-run the narrow-width accessibility audit over every cockpit page.
#
# Renders each internal/ui page component with the fixtures in
# internal/ui/narrow_test.go, serves them with the real stylesheet, and measures
# them in a headless browser at 320, 375, 640 and 768 CSS px:
#
#   WCAG 1.4.10 Reflow            content wider than the viewport, text clipped
#   WCAG 2.5.8  Target Size       a pointer target under 24x24 CSS px
#   WCAG 2.4.11 Focus Not Obscured a focused control hidden by a sticky bar
#
# Run it before merging a page that is new or has changed shape. It needs no
# Postgres and no running server — internal/ui renders standalone — but it does
# need a Chrome-family browser, which CI deliberately does not install (spec
# 032 §12). With none on the machine it prints how to point at one and exits 0.
#
# Usage: narrow-check.sh [extra go test flags]
#   LODE_NARROW_BROWSER=/path/to/chrome   use this browser instead of searching

set -euo pipefail
cd "$(dirname "$0")/.."

exec go test -trimpath -count=1 -tags narrowcheck -v \
  -run TestNarrowWidthAudit ./internal/ui/ "$@"
