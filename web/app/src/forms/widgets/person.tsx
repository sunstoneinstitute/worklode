import { useOptions } from "@/api/queries"
import { FORM_SELECT_TRIGGER } from "@/components/ui/form-fields"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { type WidgetProps, asText, inputProps } from "./types"

// Radix Select can't use "" as an item's value, so Nobody has a stand-in
const NOBODY = "__nobody__"

/** One actor id from x-options; an optional field also offers Nobody, stored as "" */
export function PersonWidget(props: WidgetProps) {
  const { value, onChange, schema, placeholder, required, fill } = props
  const url = schema["x-options"]
  const people = useOptions(url ? fill(url) : undefined)

  return (
    <Select
      value={asText(value) || (required ? "" : NOBODY)}
      onValueChange={(next) => onChange(next === NOBODY ? "" : next)}
      disabled={!people.isSuccess}
    >
      <SelectTrigger {...inputProps(props)} className={FORM_SELECT_TRIGGER}>
        <SelectValue placeholder={people.isError ? "Couldn't load people" : (placeholder ?? "Choose…")} />
      </SelectTrigger>
      <SelectContent>
        {required ? null : <SelectItem value={NOBODY}>Nobody</SelectItem>}
        {(people.data ?? []).map((person) => (
          <SelectItem key={person.id} value={person.id}>
            {person.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
