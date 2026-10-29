
Confirming Rally puts into the draft rally one task per remaining
act: every task of the spec's accepted plans that is neither landed nor
abandoned; the open planning task when a section is `unplanned`, minted if
none is open; for each `draft` covering plan a `decision` task "Accept
<plan>?" assigned to the plan's owner, minted when none is open (answering
records the decision and does not accept; Accept does). A `no_record` plan
contributes nothing. Each task is a `blocks` edge into the rally, the only edge
kind a rally carries. Adding is set-like: no duplicates, earlier-minted tasks
reused, a spec with nothing outstanding is a no-op that says so. The draft
rally is created on first add as a `rally` task in `draft` titled "Rally
<date>" owned by the viewer; a project has at most one draft rally and it is
inert until published. With members the footer shows "Rally: N tasks from M
specs". One active rally per project: a second confirm is a 409 and the footer
names the active one. Rally ranking itself is in WL-SPEC-75.

