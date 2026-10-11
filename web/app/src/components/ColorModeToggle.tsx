import { useDarkMode } from "@/hooks/use-dark-mode"
import { DarkModeIcon, LightModeIcon } from "@/components/icons"

export function ColorModeToggle() {
  const { isDark, toggle } = useDarkMode()

  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={isDark ? "Switch to light mode" : "Switch to dark mode"}
      className="flex size-6 cursor-pointer items-center justify-center text-text-primary"
    >
      {isDark ? <LightModeIcon size={22} /> : <DarkModeIcon size={22} />}
    </button>
  )
}
