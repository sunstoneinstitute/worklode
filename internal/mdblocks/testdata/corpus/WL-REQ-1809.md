
Every link the cockpit or the CLI builds to a document reference targets the
root shortcut `/<ref>`, which redirects to the document's canonical URL. A bare
number ("spec 74") is read as a spec of the rendering page's project, and
without a project it stays text. A slug or corpus filename is never a link
target. An autolinked reference never wraps across lines. A `#rule-<n>`
fragment (WL-REQ-1653) is kept through the redirect, and every rendered rule
section in the cockpit and the web app carries the element id `rule-<n>` as
well as its `sec-<anchor>` id, so the fragment lands on the rule wherever the
document arranges it. When the document no longer arranges the rule, the page
opens at the top with a notice naming the rule and linking to its own page.
