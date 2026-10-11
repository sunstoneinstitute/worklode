import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import type { DataTableColumn, DataTableProps, TableViewState } from "@/components/ui/data-table"
import {
  searchFromViewState,
  tableSearchKeys,
  viewStateFromSearch,
} from "@/components/ui/data-table/url-state"

/** The current search params minus `drop`, keeping only string values */
function keptSearch(prev: Record<string, unknown>, drop: string[]): Record<string, string> {
  const kept: Record<string, string> = {}
  for (const [key, value] of Object.entries(prev)) {
    if (!drop.includes(key) && typeof value === "string") kept[key] = value
  }
  return kept
}

/**
 * Keeps a DataTable's sort, filters and search in the URL, so a refresh, a
 * copied link or Back restores them. Pass `enabled: false` for a table that
 * shouldn't touch the URL (the task list embedded in Home, say).
 *
 * Changes replace the current history entry: typing in search shouldn't
 * add a Back step per keystroke. The table works from a local copy so
 * typing never waits on the router; the copy follows the URL whenever the
 * URL changes for another reason (Back, Forward, a pasted link).
 */
export function useTableUrlState<T>(
  columns: DataTableColumn<T>[],
  { enabled = true }: { enabled?: boolean } = {},
): Pick<DataTableProps<T>, "viewState" | "onViewStateChange"> {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const fromUrl = useMemo(() => viewStateFromSearch(search, columns), [search, columns])
  const urlKey = JSON.stringify(fromUrl)
  const [local, setLocal] = useState(fromUrl)
  // Keys of URL states this hook wrote and hasn't seen arrive yet
  const written = useRef<string[]>([])

  useEffect(() => {
    const index = written.current.indexOf(urlKey)
    if (index !== -1) {
      // Our own write arriving: the local copy is already ahead of it
      written.current = written.current.slice(index + 1)
      return
    }
    setLocal(JSON.parse(urlKey) as TableViewState)
  }, [urlKey])

  const onViewStateChange = useCallback(
    (next: TableViewState) => {
      setLocal(next)
      const nextSearch = searchFromViewState(next, columns)
      written.current.push(JSON.stringify(viewStateFromSearch(nextSearch, columns)))
      const keys = tableSearchKeys(columns)
      void navigate({
        to: ".",
        search: (prev: Record<string, unknown>) => ({ ...keptSearch(prev, keys), ...nextSearch }),
        replace: true,
        resetScroll: false,
      })
    },
    [columns, navigate],
  )

  return enabled ? { viewState: local, onViewStateChange } : {}
}

/**
 * A chip category kept in the URL as `?category=`. An unknown value reads
 * as `fallback`, and choosing `fallback` removes the param, so plain links
 * stay clean. Choosing pushes a history entry, so Back returns to the
 * previous category.
 */
export function useCategoryParam<V extends string>(
  allowed: readonly V[],
  fallback: V,
): [V, (next: V) => void] {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const raw = search.category
  const value = allowed.includes(raw as V) ? (raw as V) : fallback

  const setValue = useCallback(
    (next: V) =>
      void navigate({
        to: ".",
        search: (prev: Record<string, unknown>) => {
          const kept = keptSearch(prev, ["category"])
          return next === fallback ? kept : { ...kept, category: next }
        },
        resetScroll: false,
      }),
    [navigate, fallback],
  )

  return [value, setValue]
}
