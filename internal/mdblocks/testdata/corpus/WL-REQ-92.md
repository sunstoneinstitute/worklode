
`Transition` ends by resolving the parent, if any, with the same `tx`, `now` and `eventID`, so the child's event attributes the parent's move and no call site can forget it. Recursion ends at depth 2.

