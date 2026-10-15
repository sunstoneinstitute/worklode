/**
 * The "folder tab" curve used by ProjectToolbar and the mobile menu's
 * project card, inline so its fill can be the theme's bg-background
 * (fill-background) — it matches the page background in dark mode too.
 * preserveAspectRatio xMaxYMid slice = object-cover + object-right: it
 * fills its box, keeping the curved right end. The curve itself is a
 * transparent cutout, so whatever is behind the box shows through it.
 */
export function FolderCurve({ className = "" }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 712 50"
      preserveAspectRatio="xMaxYMid slice"
      className={`pointer-events-none fill-background ${className}`}
    >
      <path d="M-10 50H712C687 50 672.663 47.7063 661.5 45C645 41 630.5 24 627 19C616.207 3.5808 602 0 581 0H-10V50Z" />
    </svg>
  )
}
