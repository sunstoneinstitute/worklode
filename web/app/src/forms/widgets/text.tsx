import { FORM_INPUT } from "@/components/ui/form-fields"
import { Input } from "@/components/ui/input"
import { type WidgetProps, asText, inputProps } from "./types"

export function TextWidget(props: WidgetProps) {
  return (
    <Input
      {...inputProps(props)}
      value={asText(props.value)}
      onChange={(event) => props.onChange(event.target.value)}
      placeholder={props.placeholder}
      className={FORM_INPUT}
    />
  )
}
