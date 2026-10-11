// A row of pill "chips" where exactly one is active — navigation between
// sections of the same content (e.g. document categories). Active chip is
// filled; the rest are outlined and, like every outlined button, fill with
// their border color on hover (highlight with dark text in dark mode).
export function ChipNav<V extends string>({
  label,
  options,
  value,
  onChange,
}: {
  /** Accessible name for the group, e.g. "Document category" */
  label: string
  options: readonly { value: V; label: string }[]
  value: V
  onChange: (value: V) => void
}) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((option) => {
        const active = option.value === value
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={active}
            onClick={() => onChange(option.value)}
            className={`cursor-pointer rounded-full border px-3.5 py-1 text-sm transition-colors ${
              active
                ? "border-primary bg-primary text-on-primary"
                : "border-text-primary text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
            }`}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}
