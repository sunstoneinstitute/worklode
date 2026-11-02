import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { useDraft } from "./use-draft"

const KEY = "worklode:v1:draft:ada:task:WL-1"
const initial = { title: "saved", kind: "feature", rows: [] as unknown[] }
const open = (extra: { base?: string; locked?: Record<string, string>; disabled?: boolean } = {}) =>
  renderHook(() => useDraft({ actor: "ada", form: "task", recordId: "WL-1", initial, ...extra }))
const store = (value: unknown) => localStorage.setItem(KEY, JSON.stringify(value))
const stored = () => JSON.parse(localStorage.getItem(KEY) ?? "null")

beforeEach(() => localStorage.clear())
afterEach(() => vi.useRealTimers())

describe("useDraft", () => {
  it("writes values of any type with base and saved_at", () => {
    vi.useFakeTimers()
    const { result } = open({ base: "t1" })
    act(() => result.current.setValues((v) => ({ ...v, rows: [{ a: 1 }] })))
    act(() => vi.advanceTimersByTime(500))
    expect(stored()).toMatchObject({ values: { rows: [{ a: 1 }] }, base: "t1" })
    expect(stored().saved_at).toEqual(expect.any(String))
  })

  it("restores a draft whose base matches", () => {
    store({ values: { title: "mine", rows: [{ a: 1 }] }, base: "t1", saved_at: "x" })
    const { result } = open({ base: "t1" })
    expect(result.current.values).toMatchObject({ title: "mine", rows: [{ a: 1 }] })
    expect(result.current.restored).toBe(true)
  })

  it("restores a draft with no base, including the old flat form", () => {
    store({ title: "flat" })
    expect(open({ base: "t1" }).result.current.values.title).toBe("flat")
  })

  it("ignores a value of the wrong type", () => {
    store({ values: { title: 3, rows: "no" }, base: "t1" })
    expect(open({ base: "t1" }).result.current.values).toEqual(initial)
  })

  it("keeps a draft from before a newer save as an older draft", () => {
    store({ values: { ...initial, title: "mine" }, base: "t0", saved_at: "2026-11-01T10:00:00Z" })
    const { result } = open({ base: "t1" })
    expect(result.current.values).toEqual(initial)
    expect(result.current.restored).toBe(false)
    expect(result.current.older).toMatchObject({ values: { title: "mine" }, saved_at: "2026-11-01T10:00:00Z" })
    expect(stored().older.values.title).toBe("mine")
  })

  it("keeps the older draft while a new one is written, until it is discarded", () => {
    vi.useFakeTimers()
    store({ values: { ...initial, title: "mine" }, base: "t0", saved_at: "s" })
    const { result } = open({ base: "t1" })
    act(() => result.current.setValues((v) => ({ ...v, title: "new" })))
    act(() => vi.advanceTimersByTime(500))
    expect(stored()).toMatchObject({ values: { title: "new" }, older: { values: { title: "mine" } } })
    act(() => result.current.discardOlder())
    expect(result.current.older).toBeNull()
    expect(stored().older).toBeUndefined()
  })

  it("deletes an older draft whose values equal the record", () => {
    store({ values: initial, base: "t0", saved_at: "s" })
    expect(open({ base: "t1" }).result.current.older).toBeNull()
    expect(localStorage.getItem(KEY)).toBeNull()
  })

  it("does not restore a locked field, and says why", () => {
    store({ values: { ...initial, title: "mine", kind: "bug" }, base: "t1" })
    const { result } = open({ base: "t1", locked: { kind: "kind cannot change once set" } })
    expect(result.current.values).toMatchObject({ title: "mine", kind: "feature" })
    expect(result.current.notRestored).toEqual([["kind", "kind cannot change once set"]])
  })

  it("keeps no draft when disabled", () => {
    vi.useFakeTimers()
    store({ values: { ...initial, title: "mine" }, base: "t1" })
    const { result } = open({ base: "t1", disabled: true })
    expect(result.current.values).toEqual(initial)
    localStorage.clear()
    act(() => result.current.setValues((v) => ({ ...v, title: "x" })))
    act(() => vi.advanceTimersByTime(500))
    expect(localStorage.getItem(KEY)).toBeNull()
  })
})
