import type { Doc } from "@/api/types"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

/** A read-only list of backbone documents; they are written through `lode doc` (WL-REQ-1805) */
export function BackboneDocList({ docs, empty }: { docs: Doc[]; empty: string }) {
  if (docs.length === 0) return <p className="py-5 text-sm text-text-primary">{empty}</p>
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Ref</TableHead>
          <TableHead>Title</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Updated</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {docs.map((doc) => (
          <TableRow key={doc.ref}>
            <TableCell>
              <a href={`/${doc.ref}`} className="underline-offset-2 hover:text-highlight hover:underline">
                {doc.ref}
              </a>
            </TableCell>
            <TableCell>{doc.title}</TableCell>
            <TableCell>{doc.status}</TableCell>
            <TableCell>{doc.updated_at?.slice(0, 10)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
