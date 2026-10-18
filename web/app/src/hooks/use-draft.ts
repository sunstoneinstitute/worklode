import { useEffect, useRef, useState } from "react"

const PREFIX = "worklode:v1:draft:"
const WRITE_DELAY_MS = 500

/** Deletes every form draft. Called on any 401, so drafts never outlive their session (WL-REQ-1813). */
export function clearDrafts() {
  try {
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith(PREFIX)) localStorage.removeItem(key)
    }
  } catch {
    // Blocked storage holds no drafts
  }
}

function draftKey(actor: string, form: string, recordId?: string) {
  return `${PREFIX}${actor}:${form}${recordId ? `:${recordId}` : ""}`
}

/** The draft's values for the fields `initial` has, or null when there is no usable draft */
function readDraft<T extends Record<string, string>>(key: string, initial: T): T | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const stored: unknown = JSON.parse(raw)
    if (!stored || typeof stored !== "object") return null
    const values = { ...initial }
    for (const field of Object.keys(initial) as (keyof T)[]) {
      const value = (stored as Record<string, unknown>)[field as string]
      if (typeof value === "string") values[field] = value as T[keyof T]
    }
    return values
  } catch {
    return null
  }
}

function writeDraft(key: string, values: Record<string, string>) {
  try {
    localStorage.setItem(key, JSON.stringify(values))
  } catch {
    // Blocked or full storage: the form still works, without a draft
  }
}

function removeDraft(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    // Nothing to remove
  }
}

/**
 * A form's unsent copy in localStorage (WL-REQ-1813): opens with the draft
 * when there is one, writes changes 500 ms after typing stops, and deletes it
 * on `clear` (a successful submit) and `discard`.
 */
export function useDraft<T extends Record<string, string>>({
  actor,
  form,
  recordId,
  initial,
}: {
  actor: string
  form: string
  recordId?: string
  initial: T
}) {
  const key = draftKey(actor, form, recordId)
  const [start] = useState(() => readDraft(key, initial))
  const [values, setValues] = useState<T>(start ?? initial)
  const [restored, setRestored] = useState(start !== null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const unchanged = JSON.stringify(values) === JSON.stringify(initial)

  useEffect(() => {
    timer.current = setTimeout(() => {
      // Values back at the start are no draft
      if (unchanged) removeDraft(key)
      else writeDraft(key, values)
    }, WRITE_DELAY_MS)
    return () => clearTimeout(timer.current)
  }, [key, values, unchanged])

  return {
    values,
    setValues,
    /** The values differ from `initial` */
    changed: !unchanged,
    /** The form opened with a draft that has not been discarded */
    restored,
    /** Delete the draft and go back to the starting values */
    discard: () => {
      clearTimeout(timer.current)
      removeDraft(key)
      setRestored(false)
      setValues(initial)
    },
    /** Delete the draft, keeping the values (after a successful submit) */
    clear: () => {
      clearTimeout(timer.current)
      removeDraft(key)
    },
  }
}
