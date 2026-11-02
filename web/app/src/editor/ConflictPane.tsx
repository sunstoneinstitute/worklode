import type { BodyConflict } from "@/api/mutations"
import { Button } from "@/components/ui/button"
import type { Blocks } from "./BodyEditor"
import { BodyView } from "./BodyView"

/** The newer stored text beside the unsaved blocks, after a 409 on save (WL-REQ-1868) */
export function ConflictPane({
  conflict,
  saving,
  onKeepMine,
  onTakeTheirs,
}: {
  conflict: BodyConflict
  saving: boolean
  onKeepMine: () => void
  onTakeTheirs: () => void
}) {
  return (
    <section aria-label="Newer stored text" className="flex flex-col gap-4">
      <p className="text-sm">The stored text changed since you opened it. This is the newer text.</p>
      <div className="flex items-center gap-4">
        <Button type="button" disabled={saving} onClick={onKeepMine}>
          Keep mine
        </Button>
        <Button type="button" variant="outline" disabled={saving} onClick={onTakeTheirs}>
          Take theirs
        </Button>
      </div>
      <BodyView key={conflict.body_sha256} blocks={(conflict.blocks ?? []) as Blocks} />
    </section>
  )
}
