import type { CSSProperties, ComponentType, ReactNode } from "react"
import { itemKey } from "../rules"
import type { FieldItem, NoteBlock } from "../types"

export interface LayoutProps {
  /** The layout's columns, holding only the items shown right now */
  columns: Record<string, (FieldItem | NoteBlock)[]>
  renderItem: (item: FieldItem | NoteBlock) => ReactNode
  /** The submit button, absent when the form has nothing to save */
  submit?: ReactNode
}

// After every mobileOrder, in their original order
const UNNUMBERED = 1000

/**
 * A main column of 8 of 12 ending in the submit, and a sidebar of 4, once the
 * form itself is 48rem wide. Narrower, the column wrappers are `contents`, so
 * their items interleave into one column by --mobile-order.
 */
export function MainSidebarLayout({ columns, renderItem, submit }: LayoutProps) {
  const main = columns.main ?? []
  const sidebar = columns.sidebar ?? []
  const place = (items: (FieldItem | NoteBlock)[], offset: number) =>
    items.map((item, index) => (
      <div
        key={itemKey(item, index)}
        style={{ "--mobile-order": item.mobileOrder ?? UNNUMBERED + offset + index } as CSSProperties}
        className="order-(--mobile-order) @3xl:order-none"
      >
        {renderItem(item)}
      </div>
    ))

  return (
    <div className="flex flex-col gap-5 @3xl:grid @3xl:grid-cols-12 @3xl:items-start @3xl:gap-x-10 @3xl:gap-y-5">
      <div className="contents @3xl:col-span-8 @3xl:flex @3xl:flex-col @3xl:gap-5">
        {place(main, 0)}
        {submit ? <div className="order-last self-start">{submit}</div> : null}
      </div>
      <div className="contents @3xl:col-span-4 @3xl:flex @3xl:flex-col @3xl:gap-5">{place(sidebar, main.length)}</div>
    </div>
  )
}

/** One full-width column, `main`, then the submit */
export function SingleLayout({ columns, renderItem, submit }: LayoutProps) {
  return (
    <div className="flex flex-col gap-6">
      {(columns.main ?? []).map((item, index) => (
        <div key={itemKey(item, index)}>{renderItem(item)}</div>
      ))}
      {submit ? <div className="self-start">{submit}</div> : null}
    </div>
  )
}

export function layoutFor(type: string): ComponentType<LayoutProps> {
  return type === "main-sidebar" ? MainSidebarLayout : SingleLayout
}
