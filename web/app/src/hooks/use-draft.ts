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

type Values = Record<string, unknown>
type Older = { values: Values; saved_at: string }
/** What a key holds. `base` is the record's `updated_at` the form opened with (WL-REQ-1891). */
type Stored = { values?: Values; base?: string; saved_at?: string; older?: Older }

const isMap = (value: unknown): value is object => typeof value === "object" && value !== null && !Array.isArray(value)

/** The stored draft, or null when there is no usable one. An old draft is the flat value map. */
function readStored(key: string): Stored | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const stored: unknown = JSON.parse(raw)
    if (!stored || typeof stored !== "object") return null
    if (!("values" in stored || "older" in stored)) return { values: stored as Values }
    const { values, base, saved_at, older } = stored as Stored
    return {
      values: isMap(values) ? values : undefined,
      base,
      saved_at,
      older: isMap(older) && isMap(older.values) ? older : undefined,
    }
  } catch {
    return null
  }
}

function writeStored(key: string, stored: Stored) {
  try {
    if (stored.values || stored.older) localStorage.setItem(key, JSON.stringify(stored))
    else localStorage.removeItem(key)
  } catch {
    // Blocked or full storage: the form still works, without a draft
  }
}

const sameKind = (a: unknown, b: unknown) => Array.isArray(a) === Array.isArray(b) && typeof a === typeof b
const differs = (a: unknown, b: unknown) => JSON.stringify(a) !== JSON.stringify(b)

/** `initial` with the draft's values for the fields it has, where the type still fits */
function fill<T extends Values>(initial: T, stored: Values): T {
  const values: Values = { ...initial }
  for (const field of Object.keys(initial)) {
    if (sameKind(stored[field], initial[field])) values[field] = stored[field]
  }
  return values as T
}

/** What the form opens with: a draft to restore, and an older one to offer */
function openDraft<T extends Values>(key: string, initial: T, base?: string, locked: Record<string, string> = {}) {
  const stored = readStored(key)
  let older = stored?.older && differs(fill(initial, stored.older.values), initial) ? stored.older : undefined
  let values = stored?.values ? fill(initial, stored.values) : null
  // Saved since the draft was written: never put it over the newer save
  if (values && stored?.base && base && stored.base !== base) {
    if (differs(values, initial)) older = { values, saved_at: stored.saved_at ?? "" }
    values = null
  }
  const notRestored: [string, string][] = []
  if (values) {
    for (const [field, reason] of Object.entries(locked)) {
      if (!(field in initial) || !differs((values as Values)[field], initial[field])) continue
      ;(values as Values)[field] = initial[field]
      notRestored.push([field, reason])
    }
  }
  if (stored) writeStored(key, values ? { ...stored, values, older } : { older })
  return { values, older: older ?? null, notRestored }
}

/**
 * A form's unsent copy in localStorage (WL-REQ-1813, WL-REQ-1891): opens with
 * the draft unless the record was saved since, which keeps it as `older`,
 * writes changes 500 ms after typing stops, and deletes it on `clear` (a
 * successful submit) and `discard`. A field in `locked` is not restored.
 * `disabled` keeps no draft at all.
 */
export function useDraft<T extends Values>({
  actor,
  form,
  recordId,
  initial,
  base,
  locked,
  disabled = false,
}: {
  actor: string
  form: string
  recordId?: string
  initial: T
  base?: string
  locked?: Record<string, string>
  disabled?: boolean
}) {
  const key = draftKey(actor, form, recordId)
  const [start] = useState(() => (disabled ? { values: null, older: null, notRestored: [] } : openDraft(key, initial, base, locked)))
  const [values, setValues] = useState<T>(start.values ?? initial)
  const [restored, setRestored] = useState(start.values !== null)
  const [older, setOlder] = useState<Older | null>(start.older)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const unchanged = !differs(values, initial)
  const stored = (next?: T): Stored => ({
    ...(next ? { values: next, base, saved_at: new Date().toISOString() } : {}),
    older: older ?? undefined,
  })

  useEffect(() => {
    if (disabled) return
    timer.current = setTimeout(() => {
      // Values back at the start are no draft
      writeStored(key, stored(unchanged ? undefined : values))
    }, WRITE_DELAY_MS)
    return () => clearTimeout(timer.current)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `stored` reads what these name
  }, [key, values, unchanged, older, base, disabled])

  return {
    values,
    setValues,
    /** The values differ from `initial` */
    changed: !unchanged,
    /** The form opened with a draft that has not been discarded */
    restored,
    /** The restored draft's locked fields and why they were not restored */
    notRestored: start.notRestored,
    /** A draft from before a newer save, offered until it is discarded */
    older,
    discardOlder: () => {
      clearTimeout(timer.current)
      setOlder(null)
      writeStored(key, unchanged ? {} : { values, base, saved_at: new Date().toISOString() })
    },
    /** Delete the draft and go back to the starting values */
    discard: () => {
      clearTimeout(timer.current)
      writeStored(key, stored())
      setRestored(false)
      setValues(initial)
    },
    /** Write these values now, without the typing delay (a lost lock, a closing page) */
    flush: (next: T) => {
      clearTimeout(timer.current)
      writeStored(key, stored(next))
      setValues(next)
    },
    /** Delete the draft, keeping the values (after a successful submit) */
    clear: () => {
      clearTimeout(timer.current)
      writeStored(key, stored())
    },
  }
}
