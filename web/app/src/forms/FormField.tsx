import { useId } from "react"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { type Blocks } from "@/editor/BodyEditor"
import { BodyView } from "@/editor/BodyView"
import { displayText } from "./rules"
import type { FieldItem, FieldSchema } from "./types"
import { widgetFor } from "./widgets"

/**
 * One field: its label, its widget, its help text and its error. A field the
 * server locks is text with its reason, with no control (WL-REQ-1890).
 */
export function FormField({
  item,
  schema,
  value,
  onChange,
  error,
  required,
  lockedReason,
  readOnly,
  cellErrors,
  fill,
}: {
  item: FieldItem
  schema: FieldSchema
  value: unknown
  onChange: (value: unknown) => void
  error?: string
  required: boolean
  lockedReason?: string
  readOnly?: boolean
  cellErrors?: Record<string, string>
  fill: (url: string) => string
}) {
  const id = useId()
  const Widget = widgetFor(item, schema)
  const label = schema.title ?? item.field
  const errorId = error ? `${id}-error` : undefined
  const descriptionId = schema.description ? `${id}-description` : undefined
  const describedBy = [descriptionId, errorId].filter(Boolean).join(" ") || undefined

  const locked =
    item.widget === "richtext" ? (
      <BodyView blocks={(value ?? []) as Blocks} />
    ) : item.widget === "rows" ? null : (
      <p id={id} className="text-sm" title={lockedReason}>
        {displayText(schema, value) || "—"}
      </p>
    )

  return (
    <Field data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id} className={item.hideLabel ? "sr-only" : undefined}>
        {label}
      </FieldLabel>
      {lockedReason && locked ? (
        locked
      ) : (
        // oxlint-disable-next-line react/static-components -- widgetFor picks from a fixed set of module-level components
        <Widget
          id={id}
          label={label}
          value={value}
          onChange={onChange}
          schema={schema}
          placeholder={item.placeholder}
          invalid={Boolean(error)}
          required={required}
          describedBy={describedBy}
          item={item}
          readOnly={readOnly || Boolean(lockedReason)}
          cellErrors={cellErrors}
          fill={fill}
        />
      )}
      {lockedReason ? <p className="text-xs text-muted-foreground">{lockedReason}</p> : null}
      {schema.description ? <FieldDescription id={descriptionId}>{schema.description}</FieldDescription> : null}
      {error ? <FieldError id={errorId}>{error}</FieldError> : null}
    </Field>
  )
}
