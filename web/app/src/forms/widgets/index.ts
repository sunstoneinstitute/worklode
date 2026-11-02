import type { ComponentType } from "react"
import type { FieldItem, FieldSchema } from "../types"
import { ClearableWidget } from "./clearable"
import { DateWidget } from "./date"
import { PeopleWidget } from "./people"
import { PersonWidget } from "./person"
import { RichTextWidget } from "./richtext"
import { RowsWidget } from "./rows"
import { SelectWidget } from "./select"
import { TextWidget } from "./text"
import { TextareaWidget } from "./textarea"
import type { WidgetProps } from "./types"

// A new widget also joins the enum in form-config.schema.json.
const WIDGETS: Record<string, ComponentType<WidgetProps>> = {
  text: TextWidget,
  textarea: TextareaWidget,
  clearable: ClearableWidget,
  select: SelectWidget,
  date: DateWidget,
  person: PersonWidget,
  people: PeopleWidget,
  richtext: RichTextWidget,
  rows: RowsWidget,
}

/** The item's widget (an unknown name renders as text), else the one that fits the schema */
export function widgetFor(item: FieldItem, schema: FieldSchema): ComponentType<WidgetProps> {
  if (item.widget) return WIDGETS[item.widget] ?? TextWidget
  if (schema.oneOf) return SelectWidget
  if (schema.format === "date") return DateWidget
  if (schema["x-options"]) return PersonWidget
  return TextWidget
}
