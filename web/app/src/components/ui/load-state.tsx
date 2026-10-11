import { Button } from "@/components/ui/button"

/** A pulsing placeholder the size of the content it stands in for */
export function LoadingBlock({ label, className = "" }: { label: string; className?: string }) {
  return (
    <div
      role="status"
      aria-label={label}
      className={`w-full animate-pulse rounded-card bg-surface ${className}`}
    />
  )
}

/** A section that failed to load, with a way to try again */
export function LoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="flex w-full flex-wrap items-center gap-4 rounded-card bg-surface p-5 text-sm text-text-primary">
      <p className="flex-1">{message}</p>
      <Button
        variant="outline"
        size="sm"
        onClick={onRetry}
        // Same outlined style as the tables' toolbar buttons
        className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
      >
        Retry
      </Button>
    </div>
  )
}
