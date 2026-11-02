import { FORM_TEXTAREA } from "@/components/ui/form-fields"
import { Textarea } from "@/components/ui/textarea"
import { type WidgetProps, asText, inputProps } from "./types"

export function TextareaWidget(props: WidgetProps) {
  return (
    <Textarea
      {...inputProps(props)}
      value={asText(props.value)}
      onChange={(event) => props.onChange(event.target.value)}
      placeholder={props.placeholder}
      className={`min-h-40 ${FORM_TEXTAREA}`}
    />
  )
}
