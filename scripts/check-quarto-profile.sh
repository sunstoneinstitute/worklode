#!/usr/bin/env bash
# Renders internal/mdblocks/testdata/profile.md, one instance of every
# Worklode Markdown entry (WL-REQ-1871), through Quarto. Fails on any warning
# or when a construct is missing from the HTML. Run by hand where `quarto` is
# installed; it is not a CI job.
#
#   scripts/check-quarto-profile.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Quarto renders .md only without executable cells; the mermaid cell needs .qmd.
cp "$root/internal/mdblocks/testdata/profile.md" "$work/profile.qmd"
cp "$root/internal/mdblocks/testdata/pixel.svg" "$work/"
log=$(cd "$work" && quarto render profile.qmd --to html 2>&1)
if grep -qiE 'warn|error' <<<"$log"; then
	printf '%s\n' "$log" >&2
	echo "check-quarto-profile: quarto reported a warning" >&2
	exit 1
fi

html="$work/profile.html"
fail=0
check() { # name, fixed string the construct renders as
	grep -qF -- "$2" "$html" || { echo "check-quarto-profile: missing $1 ($2)" >&2; fail=1; }
}
check paragraph '<p>A paragraph'
check heading '<h2'
check 'heading attribute' 'id="purpose"'
check 'bullet list item' '<li>bullet item'
check 'numbered list item' '<ol'
check 'task list item' '<input type="checkbox" checked'
check table '<table'
check 'fenced code' 'class="sourceCode go'
check mermaid '<pre class="mermaid'
check 'block quote' '<blockquote'
check callout 'callout-warning'
check image '<img src="pixel.svg"'
check emphasis '<em>emphasis</em>'
check strong '<strong>strong</strong>'
check strikethrough '<del>struck</del>'
check 'inline code' '<code>code</code>'
check link '<a href="https://example.com/">'
check emoji '🚀'
exit $fail
