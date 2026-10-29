import { useCallback, useEffect, useRef, useState } from "react"
import {
  type BodyLock,
  type LockKind,
  type LockLost,
  checkInBodyLock,
  releaseBodyLock,
  takeBodyLock,
} from "@/api/mutations"

export const LOCK_CHECK_IN_MS = 60_000

export type BodyLockState =
  | { status: "taking" }
  | { status: "held"; session: string; touch: () => void; lose: (lost: LockLost) => void }
  | { status: "locked"; lock: BodyLock; retry: () => void }
  | { status: "lost"; lost: LockLost; retry: () => void }
  | { status: "failed"; retry: () => void }

type Outcome =
  | { status: "taking" }
  | { status: "held"; session: string }
  | { status: "locked"; lock: BodyLock }
  | { status: "lost"; lost: LockLost }
  | { status: "failed" }

// Per subject, the last take or release still in flight. A new take waits
// for it, so a cancelled take's release (StrictMode, or leaving and quickly
// reopening) can't remove the lock the next take just got.
const pending = new Map<string, Promise<unknown>>()

/**
 * Takes a body's lock on mount, checks in every minute while held with
 * `dirty` and the last activity (call `touch()` on typing and clicking),
 * and releases it on unmount and on pagehide, with or without unsaved
 * changes (WL-REQ-1868). On a pagehide with `dirty`, `onBeforeRelease`
 * runs first, so the page can keep its blocks as a browser draft. A 409
 * check-in, or `lose()` after a refused save, moves to `lost`; `retry()`
 * takes the lock again.
 */
export function useBodyLock(
  kind: LockKind,
  id: string,
  { dirty, onBeforeRelease }: { dirty: boolean; onBeforeRelease?: () => void },
): BodyLockState {
  const [outcome, setOutcome] = useState<Outcome>({ status: "taking" })
  const [attempt, setAttempt] = useState(0)
  const dirtyRef = useRef(dirty)
  const beforeReleaseRef = useRef(onBeforeRelease)
  const lastActivity = useRef(0)

  useEffect(() => {
    dirtyRef.current = dirty
    beforeReleaseRef.current = onBeforeRelease
  })

  const touch = useCallback(() => {
    lastActivity.current = Date.now()
  }, [])
  const retry = useCallback(() => {
    setOutcome({ status: "taking" })
    setAttempt((count) => count + 1)
  }, [])
  // Set while a lock is held: reports it gone, e.g. when a save is refused
  const loseRef = useRef<(lost: LockLost) => void>(() => {})
  const lose = useCallback((lost: LockLost) => loseRef.current(lost), [])

  useEffect(() => {
    const key = `${kind}:${id}`
    let cancelled = false
    let timer: ReturnType<typeof setInterval> | undefined
    // This page's hold is over, by its own release or a lost lock: nothing is left to DELETE
    let released = false
    let held = false
    let session = ""
    let leftOnPageHide = false
    lastActivity.current = Date.now()

    const lost = (answer: LockLost) => {
      if (cancelled || !held) return
      clearInterval(timer)
      held = false
      released = true
      setOutcome({ status: "lost", lost: answer })
    }

    const onPageHide = () => {
      if (!held) return
      if (dirtyRef.current) beforeReleaseRef.current?.()
      clearInterval(timer)
      held = false
      released = true
      leftOnPageHide = true
      releaseBodyLock(kind, id, session, { keepalive: true }).catch(() => {})
    }

    // A page restored from the back-forward cache gave its lock up at pagehide: take it again
    const onPageShow = (event: PageTransitionEvent) => {
      if (event.persisted && leftOnPageHide && !cancelled) retry()
    }

    const taking = (pending.get(key) ?? Promise.resolve()).then(() => takeBodyLock(kind, id))
    pending.set(key, taking.catch(() => {}))

    taking
      .then((result) => {
        if (cancelled) return
        if (!result.ok) {
          setOutcome({ status: "locked", lock: result.lock })
          return
        }
        held = true
        session = result.lock.session ?? ""
        loseRef.current = lost
        setOutcome({ status: "held", session })
        timer = setInterval(() => {
          checkInBodyLock(kind, id, {
            session,
            dirty: dirtyRef.current,
            last_activity: new Date(lastActivity.current).toISOString(),
          })
            .then((answer) => {
              if (!answer.ok) lost(answer.lost)
            })
            .catch(() => {
              // A failed check-in is tried again next minute
            })
        }, LOCK_CHECK_IN_MS)
        window.addEventListener("pagehide", onPageHide)
        window.addEventListener("pageshow", onPageShow)
      })
      .catch(() => {
        if (!cancelled) setOutcome({ status: "failed" })
      })

    return () => {
      cancelled = true
      clearInterval(timer)
      window.removeEventListener("pagehide", onPageHide)
      window.removeEventListener("pageshow", onPageShow)
      // Release once the take has settled, and make the next take wait for it
      pending.set(
        key,
        taking
          .then((result) => {
            if (result.ok && !released) return releaseBodyLock(kind, id, result.lock.session ?? "")
          })
          .catch(() => {}),
      )
    }
  }, [kind, id, attempt, retry])

  switch (outcome.status) {
    case "held":
      return { ...outcome, touch, lose }
    case "locked":
    case "lost":
    case "failed":
      return { ...outcome, retry }
    default:
      return outcome
  }
}
