import { useOptions } from "@/api/queries"
import { CheckIcon } from "@/components/icons"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { FORM_SELECT_TRIGGER } from "@/components/ui/form-fields"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { cn } from "cn"
import { type WidgetProps, inputProps } from "./types"

/** A list of actor ids from x-options: each person toggles in or out, and the list stays open */
export function PeopleWidget(props: WidgetProps) {
  const { label, value, onChange, schema, fill } = props
  const url = schema["x-options"]
  const people = useOptions(url ? fill(url) : undefined)
  const options = people.data ?? []
  const ids = Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : []
  const nameOf = (id: string) => options.find((person) => person.id === id)?.name ?? id
  const shown = ids.length === 0 ? "No one" : ids.map(nameOf).join(", ")
  // Someone chosen and no longer listed is in the value, so stays removable
  const listed = [...options, ...ids.filter((id) => !options.some((person) => person.id === id)).map((id) => ({ id, name: id }))]

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          {...inputProps(props)}
          aria-label={`${label}: ${shown}`}
          disabled={!people.isSuccess}
          className={cn(FORM_SELECT_TRIGGER, "flex cursor-pointer items-center rounded-md border border-input text-left text-sm disabled:opacity-50 aria-invalid:border-destructive")}
        >
          <span className="truncate">{people.isError ? "Couldn't load people" : shown}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find someone..." />
          <CommandList>
            <CommandEmpty>No one found.</CommandEmpty>
            <CommandGroup>
              {listed.map((person) => (
                <CommandItem
                  key={person.id}
                  value={person.name}
                  onSelect={() => onChange(ids.includes(person.id) ? ids.filter((id) => id !== person.id) : [...ids, person.id])}
                >
                  <span className="flex w-4 items-center justify-center">{ids.includes(person.id) ? <CheckIcon size={14} /> : null}</span>
                  {person.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
