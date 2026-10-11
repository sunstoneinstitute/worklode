import { type ReactNode, useState } from "react"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { initials } from "@/lib/initials"
import { Badge } from "@/components/ui/badge"
import { Calendar } from "@/components/ui/calendar"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { CheckIcon } from "@/components/icons"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import type { DataTableUser, SelectOption, RowAction, RowActionHelpers } from "./types"

export function TextCell({ value }: { value: unknown }) {
  if (value === null || value === undefined) return null
  return <>{String(value)}</>
}

export function badgeStyle(option: SelectOption | undefined) {
  if (!option?.colorVar) {
    return { className: "bg-muted text-muted-foreground", style: undefined }
  }
  const hue = `var(${option.colorVar})`
  return {
    // Light mode: an 80% fill of the option's accent with dark text. Dark
    // mode: a faint tint of the accent, text in a lighter shade of it.
    // The --badge-* tokens (and contrast numbers) are in index.css.
    className: undefined,
    style: {
      backgroundColor: `color-mix(in srgb, ${hue} var(--badge-fill-opacity), transparent)`,
      color: `color-mix(in srgb, ${hue} var(--badge-text-hue), var(--badge-text-base))`,
    },
  }
}

export function SelectCell({
  value,
  options,
  onChange,
}: {
  value: string
  options: SelectOption[]
  onChange: (next: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = options.find((option) => option.value === value)
  const { className, style } = badgeStyle(selected)

  // A field that doesn't apply to this row (e.g. Story Type on a Component)
  // shows nothing, not an empty chip
  if (!value) return null

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        {/* Only the Badge gets backgroundColor — it's the element with
            rounded-4xl corners. The button is a plain rectangle; painting
            the fill there too showed square corners poking out from behind
            the badge's rounded ones. */}
        <button type="button" className="cursor-pointer">
          <Badge className={className} style={style}>
            {selected?.label ?? value}
          </Badge>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-44 p-0" align="start">
        <Command>
          <CommandList>
            <CommandEmpty>No options.</CommandEmpty>
            <CommandGroup>
              {options.map((option) => (
                <CommandItem
                  key={option.value}
                  value={option.value}
                  onSelect={() => {
                    onChange(option.value)
                    setOpen(false)
                  }}
                >
                  {option.label}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

const DATE_FORMAT = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  year: "numeric",
  timeZone: "UTC",
})

/** ISO (YYYY-MM-DD) is parsed as UTC; formatting in UTC too keeps the day from shifting. */
export function formatIsoDate(iso: string) {
  if (!iso) return ""
  return DATE_FORMAT.format(new Date(`${iso}T00:00:00Z`))
}

export function toIsoDate(date: Date) {
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-")
}

export function DateCell({
  value,
  onChange,
}: {
  value: string
  onChange: (nextIso: string) => void
}) {
  const [open, setOpen] = useState(false)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="cursor-pointer text-left">
          {formatIsoDate(value)}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <Calendar
          mode="single"
          selected={value ? new Date(`${value}T00:00:00Z`) : undefined}
          onSelect={(date) => {
            if (date) onChange(toIsoDate(date))
            setOpen(false)
          }}
        />
      </PopoverContent>
    </Popover>
  )
}

/**
 * How a person appears everywhere in the table — cell, picker, filter,
 * search, sort: "@" + their handle, or their name with spaces and
 * punctuation stripped when they don't have one ("Tobias Smith" →
 * "@TobiasSmith").
 */
export function userHandle(user: DataTableUser): string {
  return `@${user.handle ?? user.name.replace(/[^\p{L}\p{N}]/gu, "")}`
}

export function UserCell({
  value,
  users,
  onChange,
}: {
  value: string
  users: DataTableUser[]
  onChange: (nextUserId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const selected = users.find((user) => user.id === value)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="flex cursor-pointer items-center gap-2 text-left">
          {selected ? (
            <>
              <Avatar className="size-6">
                {selected.avatarUrl ? <AvatarImage src={selected.avatarUrl} alt="" /> : null}
                <AvatarFallback className="text-xs">{initials(selected.name)}</AvatarFallback>
              </Avatar>
              {userHandle(selected)}
            </>
          ) : (
            "Unassigned"
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find someone..." />
          <CommandList>
            <CommandEmpty>No one found.</CommandEmpty>
            <CommandGroup>
              {users.map((user) => (
                <CommandItem
                  key={user.id}
                  // Findable by handle or full name
                  value={`${userHandle(user)} ${user.name}`}
                  onSelect={() => {
                    onChange(user.id)
                    setOpen(false)
                  }}
                >
                  {userHandle(user)}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

const USERS_SHOWN = 3

/**
 * Several people at once (e.g. meeting participants): a row of avatars —
 * the first few, then "+N" — opening a picker where each person toggles
 * in or out. The picker stays open while you tick several.
 */
export function UsersCell({
  value,
  users,
  onChange,
}: {
  value: string[]
  users: DataTableUser[]
  onChange: (nextUserIds: string[]) => void
}) {
  const selected = users.filter((user) => value.includes(user.id))
  const shown = selected.slice(0, USERS_SHOWN)
  const extra = selected.length - shown.length
  const toggle = (id: string) =>
    onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id])

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={
            selected.length ? `Participants: ${selected.map(userHandle).join(", ")}` : "No participants"
          }
          className="flex cursor-pointer items-center gap-1 text-left"
        >
          {selected.length === 0 ? "No one" : null}
          {shown.map((user) => (
            <Avatar key={user.id} className="size-6" title={userHandle(user)}>
              {user.avatarUrl ? <AvatarImage src={user.avatarUrl} alt="" /> : null}
              <AvatarFallback className="text-xs">{initials(user.name)}</AvatarFallback>
            </Avatar>
          ))}
          {extra > 0 ? <span className="pl-0.5 text-xs text-muted-foreground">+{extra}</span> : null}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-56 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find someone..." />
          <CommandList>
            <CommandEmpty>No one found.</CommandEmpty>
            <CommandGroup>
              {users.map((user) => (
                <CommandItem
                  key={user.id}
                  value={`${userHandle(user)} ${user.name}`}
                  onSelect={() => toggle(user.id)}
                >
                  <span className="flex w-4 items-center justify-center">
                    {value.includes(user.id) ? <CheckIcon size={14} /> : null}
                  </span>
                  {userHandle(user)}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

const DEFAULT_CONFIRM = "Are you sure you want to delete this item?"

/** The question an action asks before running, or null if it just runs. */
function confirmMessage<T>(action: RowAction<T>, row: T) {
  if (action.confirm === false) return null
  if (typeof action.confirm === "function") return action.confirm(row)
  if (typeof action.confirm === "string") return action.confirm
  return action.variant === "destructive" ? DEFAULT_CONFIRM : null
}

export function ActionsCell<T>({
  row,
  actions,
  helpers,
  preview,
}: {
  row: T
  actions: RowAction<T>[]
  helpers: RowActionHelpers<T>
  /** Shown in the confirm dialog — a small summary of this row */
  preview?: ReactNode
}) {
  // The action waiting on a Yes in the confirm dialog, if any
  const [pending, setPending] = useState<RowAction<T> | null>(null)

  return (
    <div className="flex items-center justify-end gap-2.5">
      {actions.filter((action) => action.when?.(row) ?? true).map((action) => (
        <button
          key={action.id}
          type="button"
          aria-label={action.label}
          title={action.label}
          onClick={() =>
            confirmMessage(action, row) ? setPending(action) : action.onSelect(row, helpers)
          }
          // Every action icon rests in the text color; destructive ones
          // (delete) only turn red on hover, the rest go to highlight.
          className={`cursor-pointer text-text-primary transition-colors ${
            action.variant === "destructive" ? "hover:text-destructive" : "hover:text-highlight"
          }`}
        >
          {action.icon}
        </button>
      ))}
      <ConfirmDialog
        open={pending !== null}
        message={pending ? (confirmMessage(pending, row) ?? "") : ""}
        onConfirm={() => pending?.onSelect(row, helpers)}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        destructive={pending?.variant === "destructive"}
      >
        {preview}
      </ConfirmDialog>
    </div>
  )
}
