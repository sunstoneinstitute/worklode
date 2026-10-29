
A question is read-only and server-enforced: while a review has an open
`question` thread the author may reply, and a round submitted by the author is
refused. If the question really asks for a change, the author edits and opens
a new round, a different act. A question is answered synchronously in-thread by
the agent that authored the change and still holds the lease; nothing is
minted, the lease is kept, the task stays `in_review`. If the author cannot
answer because the plan or spec does not cover the case, `lode task escalate`
applies: the review stays open and the thread carries the escalation's task
id, so the reviewer sees why the answer is late. A question asked of an author
whose lease expired stays open and folds into the next round.

