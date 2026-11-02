import { CloseIcon } from "@/components/icons"
import { FORM_INPUT_GROUP, FORM_INPUT_GROUP_INPUT } from "@/components/ui/form-fields"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { type WidgetProps, asText, inputProps } from "./types"

/** Text with a clear button, shown once there is something to clear */
export function ClearableWidget(props: WidgetProps) {
  const text = asText(props.value)
  return (
    <InputGroup className={FORM_INPUT_GROUP}>
      <InputGroupInput
        {...inputProps(props)}
        value={text}
        onChange={(event) => props.onChange(event.target.value)}
        placeholder={props.placeholder}
        className={FORM_INPUT_GROUP_INPUT}
      />
      {text ? (
        <InputGroupAddon align="inline-end">
          <InputGroupButton aria-label={`Clear ${props.label}`} size="icon-xs" onClick={() => props.onChange("")}>
            <CloseIcon size={16} />
          </InputGroupButton>
        </InputGroupAddon>
      ) : null}
    </InputGroup>
  )
}
