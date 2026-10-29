
A plan needs execution when its status is `accepted` and at least one live task its acceptance minted is not closed. An accepted plan that minted no tasks is not listed. When the last minted task closes, the plan moves to `executed` in the same transaction and leaves the list. Task closure uses the same `taskClosed` predicate as the ready set and blocking.
