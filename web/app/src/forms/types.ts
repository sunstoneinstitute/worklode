// A form config: web/app/src/forms/configs/<form>.json, checked against
// form-config.schema.json. WL-REQ-1888 lists the JSON Schema keywords
// `schema` may use, and WL-REQ-1893 the widgets and layouts.

export interface SchemaOption {
  const: string
  title: string
}

export interface FieldSchema {
  type?: string
  title?: string
  description?: string
  format?: string
  minLength?: number
  oneOf?: SchemaOption[]
  readOnly?: boolean
  errorMessage?: string
  items?: { type?: string; title?: string; required?: string[]; properties?: Record<string, FieldSchema> }
  /** A boolean row property: at most this many rows may hold it */
  maxContains?: number
  /** An /api/v1 route that answers crew: the field picks people */
  "x-options"?: string
}

export interface Conditional {
  if?: { properties?: Record<string, { const?: unknown }> }
  then?: { required?: string[] }
  else?: { required?: string[] }
}

export interface FormSchema extends Conditional {
  type: "object"
  properties: Record<string, FieldSchema>
  required?: string[]
  allOf?: Conditional[]
}

export type ShowWhen = { field: string; equals: unknown } | { field: string; in: unknown[] }

export interface RowColumn {
  field: string
  title?: string
  widget?: string
  display?: "name" | "role"
  /** A radio column: turning it on turns this boolean off in the same row */
  notWith?: string
}

export interface FieldItem {
  field: string
  widget?: string
  placeholder?: string
  hideLabel?: boolean
  showWhen?: ShowWhen
  /** Place in the single column a narrow form merges into */
  mobileOrder?: number
  sendWhenChanged?: boolean
  columns?: RowColumn[]
  sortBy?: string
  addWith?: string
  lockedWhen?: string
}

export interface NoteBlock {
  block: "note"
  markdown: string
  title?: string
  collapsible?: boolean
  showWhen?: ShowWhen
  mobileOrder?: number
}

export type LayoutItem = string | FieldItem | NoteBlock

export interface FormLayout {
  type: string
  columns: Record<string, LayoutItem[]>
}

export type FormValues = Record<string, unknown>

export interface FormMode {
  title: string
  submit: { method: "POST" | "PATCH" | "PUT"; url: string; label: string }
}

export interface FormConfig {
  id: string
  create?: FormMode
  edit?: FormMode
  defaults?: FormValues
  schema: FormSchema
  layout: FormLayout
}
