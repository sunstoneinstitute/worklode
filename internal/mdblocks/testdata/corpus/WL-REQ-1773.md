
crit's `Comment` shape is adopted as contract for anchoring (anchor text
as identity, line numbers as hints, four-stage recovery: LCS remap, fuzzy match
with a minimum-length guard, whole-file scan nearest the predicted line, then
`drifted`), and a golden copy of its `Comment` and top-level JSON shapes is
checked in so upstream drift is a red test.

