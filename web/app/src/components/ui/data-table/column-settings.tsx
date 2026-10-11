import {
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
} from "@dnd-kit/core"
import {
  SortableContext,
  arrayMove,
  rectSortingStrategy,
  sortableKeyboardCoordinates,
  useSortable,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import type { Column, Table } from "@tanstack/react-table"
import { DragIndicatorIcon, VisibilityIcon, VisibilityOffIcon } from "@/components/icons"
import type { DataTableColumn } from "./types"

function schemaOf<T>(column: Column<T, unknown>) {
  return (column.columnDef.meta as { column: DataTableColumn<T> }).column
}

/**
 * The column-settings row the toolbar's "Configure columns" button reveals:
 * one pill per column, dragged to reorder (pointer, touch, or keyboard —
 * focus a pill's handle, Space to pick up, arrow keys to move, Space to
 * drop) with an eye button to show/hide it. Drives TanStack's own
 * columnOrder / columnVisibility state, which DataTable already holds.
 *
 * The actions column isn't listed: it always stays last and visible. A
 * Reset link appears once the order or visibility has been changed.
 */
export function ColumnSettings<T>({ table }: { table: Table<T> }) {
  const sensors = useSensors(
    // A few px of movement before a drag starts, so a plain click on the
    // handle doesn't register as a zero-distance drag.
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const byId = new Map(table.getAllLeafColumns().map((column) => [column.id, column] as const))
  const { columnOrder } = table.getState()
  // An empty columnOrder means "schema order" — TanStack's default.
  const orderedIds = columnOrder.length > 0 ? columnOrder : [...byId.keys()]
  const ordered = orderedIds.flatMap((id) => byId.get(id) ?? [])
  const configurable = ordered.filter((column) => schemaOf(column).type !== "actions")
  const pinned = ordered.filter((column) => schemaOf(column).type === "actions")
  const visibleCount = configurable.filter((column) => column.getIsVisible()).length
  // Anything to reset? Order differs from the schema's, or a column is hidden.
  // Schema order comes from the column defs themselves — getAllLeafColumns()
  // already reflects the current columnOrder, so it can't serve as the baseline.
  const schemaIds = table.options.columns.map((def) => def.id)
  const customized =
    ordered.some((column, index) => column.id !== schemaIds[index]) ||
    configurable.some((column) => !column.getIsVisible())

  const handleDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const ids = configurable.map((column) => column.id)
    const next = arrayMove(ids, ids.indexOf(String(active.id)), ids.indexOf(String(over.id)))
    table.setColumnOrder([...next, ...pinned.map((column) => column.id)])
  }

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
      <SortableContext items={configurable.map((column) => column.id)} strategy={rectSortingStrategy}>
        <div className="flex w-full flex-wrap items-center gap-2">
          <ul aria-label="Columns" className="flex flex-wrap gap-2">
            {configurable.map((column) => (
              <ColumnPill
                key={column.id}
                column={column}
                // Never let the last visible column be hidden — an empty
                // table header has nothing left to bring the others back from.
                canHide={visibleCount > 1}
              />
            ))}
          </ul>
          {/* Only once something's been changed — back to schema order, all shown */}
          {customized ? (
            <button
              type="button"
              onClick={() => {
                table.resetColumnOrder(true)
                table.resetColumnVisibility(true)
              }}
              className="cursor-pointer px-2 text-sm text-text-primary underline transition-colors hover:text-highlight"
            >
              Reset
            </button>
          ) : null}
        </div>
      </SortableContext>
    </DndContext>
  )
}

function ColumnPill<T>({ column, canHide }: { column: Column<T, unknown>; canHide: boolean }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: column.id,
  })
  const { label } = schemaOf(column)
  const visible = column.getIsVisible()

  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={[
        "flex items-center rounded-full border border-divider-line/40 bg-background text-sm text-text-primary",
        isDragging ? "relative z-10 shadow-md" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      {/* touch-none: lets dnd-kit's pointer sensor own touch drags instead
          of the browser treating them as a page scroll. */}
      <button
        type="button"
        aria-label={`Reorder ${label}`}
        className="flex cursor-grab touch-none items-center gap-1 py-1.5 pl-2 pr-1 active:cursor-grabbing"
        {...attributes}
        {...listeners}
      >
        <DragIndicatorIcon size={18} className="text-text-primary/60" />
        <span className={visible ? "" : "text-text-primary/50 line-through"}>{label}</span>
      </button>
      <button
        type="button"
        aria-label={visible ? `Hide ${label}` : `Show ${label}`}
        disabled={visible && !canHide}
        onClick={() => column.toggleVisibility()}
        className="flex cursor-pointer items-center py-1.5 pl-1 pr-3 transition-colors enabled:hover:text-highlight disabled:cursor-not-allowed disabled:opacity-40"
      >
        {visible ? <VisibilityIcon size={18} /> : <VisibilityOffIcon size={18} />}
      </button>
    </li>
  )
}
