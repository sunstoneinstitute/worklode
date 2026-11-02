import { FORM_INPUT } from "@/components/ui/form-fields"
import { Input } from "@/components/ui/input"
import { type WidgetProps, asText, inputProps } from "./types"

/** YYYY-MM-DD, the native date input (WL-REQ-1893) */
export function DateWidget(props: WidgetProps) {
  return (
    <Input
      {...inputProps(props)}
      type="date"
      value={asText(props.value)}
      onChange={(event) => props.onChange(event.target.value)}
      className={FORM_INPUT}
    />
  )
}
