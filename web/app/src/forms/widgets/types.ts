import type { FieldItem, FieldSchema } from "../types"

/** What every widget gets. FormField renders the label, help text and error around it. */
export interface WidgetProps {
  /** The input's id; the label points at it */
  id: string
  label: string
  value: unknown
  onChange: (value: unknown) => void
  schema: FieldSchema
  placeholder?: string
  invalid: boolean
  required: boolean
  /** The ids of the help text and error, for aria-describedby */
  describedBy?: string
  item: FieldItem
  readOnly?: boolean
  /** A `rows` field's cell errors, keyed "<index>/<property>" */
  cellErrors?: Record<string, string>
  /** An x-options route with its placeholders filled */
  fill: (url: string) => string
}

export function asText(value: unknown) {
  return value === undefined || value === null ? "" : String(value)
}

export function inputProps({ id, invalid, required, describedBy }: WidgetProps) {
  return {
    id,
    "aria-invalid": invalid || undefined,
    "aria-required": required || undefined,
    "aria-describedby": describedBy,
  }
}
