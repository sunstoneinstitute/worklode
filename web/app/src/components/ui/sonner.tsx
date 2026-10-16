import { Toaster as Sonner } from "sonner"

/**
 * Where toasts appear (mounted once, in the root layout). Colors come from
 * the design tokens, so they follow dark mode. Inline styles, because
 * sonner's own stylesheet would win over utility classes.
 */
export function Toaster() {
  return (
    <Sonner
      position="bottom-right"
      toastOptions={{
        style: {
          background: "var(--color-surface)",
          color: "var(--color-text-primary)",
          border: "none",
          borderRadius: "var(--radius-card)",
        },
      }}
    />
  )
}
