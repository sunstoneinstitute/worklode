import { useState } from "react"
import { useOpenThread, useReplyThread, useResolveThread } from "@/api/mutations"
import type { ReviewThread, ThreadSubject } from "@/api/queries"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "cn"
import type { SelectionAnchor } from "./threads"

/** Thread intents (WL-REQ-1318) */
const INTENTS = { note: "Note", change_request: "Change request", question: "Question" } as const
type Intent = keyof typeof INTENTS

/**
 * A subject's threads: each with its quote, intent, replies and resolution.
 * An obsolete thread is listed struck through, never hidden (WL-REQ-1317).
 * `pending` is the selection a new thread would anchor to.
 */
export function CommentPanel({
  subject,
  version,
  threads,
  pending,
  onOpened,
}: {
  subject: ThreadSubject
  version: number
  threads: ReviewThread[]
  pending: SelectionAnchor | null
  onOpened: () => void
}) {
  return (
    <section aria-label="Comments" className="flex flex-col gap-4 text-sm">
      <h2 className="font-medium">Comments</h2>
      {subject.openOn === undefined ? (
        <p>No document arranges this rule, so it takes no comments yet.</p>
      ) : pending ? (
        <Composer subject={subject} openOn={subject.openOn} version={version} pending={pending} onOpened={onOpened} />
      ) : (
        <p className="text-text-primary/70">Select text in the body to comment on it. A comment anchors to the text as loaded.</p>
      )}
      {threads.length === 0 ? <p className="text-text-primary/70">No comments yet.</p> : null}
      <ul className="flex flex-col gap-3">
        {threads.map((thread) => (
          <ThreadItem key={thread.id} thread={thread} />
        ))}
      </ul>
    </section>
  )
}

function Composer({ subject, openOn, version, pending, onOpened }: {
  subject: ThreadSubject
  openOn: number
  version: number
  pending: SelectionAnchor
  onOpened: () => void
}) {
  const [intent, setIntent] = useState<Intent>("note")
  const [body, setBody] = useState("")
  const open = useOpenThread()
  const submit = () => {
    const anchor = { [subject.kind]: subject.id, version, byte_start: pending.start, byte_end: pending.end, quote: pending.quote }
    open.mutate({ doc: openOn, input: { anchor, intent, body } }, { onSuccess: () => { setBody(""); onOpened() } })
  }
  return (
    <form
      aria-label="New comment"
      className="flex flex-col gap-2 rounded-lg border p-3"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <blockquote className="border-l-2 pl-2 whitespace-pre-wrap">{pending.quote}</blockquote>
      <Label htmlFor="thread-intent">Intent</Label>
      <select id="thread-intent" className="rounded-lg border bg-transparent p-2" value={intent} onChange={(e) => setIntent(e.target.value as Intent)}>
        {Object.entries(INTENTS).map(([value, label]) => (
          <option key={value} value={value}>
            {label}
          </option>
        ))}
      </select>
      <Label htmlFor="thread-body">Comment</Label>
      <Textarea id="thread-body" value={body} onChange={(e) => setBody(e.target.value)} />
      <Button type="submit" disabled={!body.trim() || open.isPending}>
        Comment
      </Button>
    </form>
  )
}

function ThreadItem({ thread }: { thread: ReviewThread }) {
  const [reply, setReply] = useState("")
  const send = useReplyThread()
  const resolve = useResolveThread()
  const id = thread.id ?? 0
  const quote = thread.anchor?.quote || (thread.anchor?.doc_anchor ? `#${thread.anchor.doc_anchor}` : "")
  return (
    <li data-thread-id={id} data-status={thread.status} className={cn("flex flex-col gap-2 rounded-lg border p-3", thread.status === "obsolete" && "line-through opacity-70")}>
      {quote ? <blockquote className="border-l-2 pl-2 whitespace-pre-wrap">{quote}</blockquote> : null}
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{INTENTS[thread.intent as Intent] ?? thread.intent}</Badge>
        <span>{thread.status === "resolved" ? `Resolved${thread.resolved_by ? ` by ${thread.resolved_by}` : ""}` : thread.status}</span>
      </div>
      {(thread.messages ?? []).map((m) => (
        <p key={m.id}>
          <span className="font-medium">{m.author}</span> {m.body}
        </p>
      ))}
      {thread.status === "open" ? (
        <form
          aria-label={`Reply to thread ${id}`}
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            send.mutate({ thread: id, body: reply }, { onSuccess: () => setReply("") })
          }}
        >
          <Textarea aria-label="Reply" value={reply} onChange={(e) => setReply(e.target.value)} />
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={!reply.trim() || send.isPending}>
              Reply
            </Button>
            <Button type="button" size="sm" variant="outline" disabled={resolve.isPending} onClick={() => resolve.mutate(id)}>
              Resolve
            </Button>
          </div>
        </form>
      ) : null}
    </li>
  )
}
