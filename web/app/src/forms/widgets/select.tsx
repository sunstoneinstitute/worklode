import { FORM_SELECT_TRIGGER } from "@/components/ui/form-fields"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { type WidgetProps, asText, inputProps } from "./types"

/** The schema's oneOf: `const` is stored, `title` is shown */
export function SelectWidget(props: WidgetProps) {
  return (
    <Select value={asText(props.value)} onValueChange={props.onChange}>
      <SelectTrigger {...inputProps(props)} className={FORM_SELECT_TRIGGER}>
        <SelectValue placeholder={props.placeholder ?? "Choose…"} />
      </SelectTrigger>
      <SelectContent>
        {(props.schema.oneOf ?? []).map((option) => (
          <SelectItem key={option.const} value={option.const}>
            {option.title}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
