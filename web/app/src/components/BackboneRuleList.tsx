import type { Rule } from "@/api/types"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

/** A read-only list of rules; they are written through `lode rule` (WL-REQ-1805) */
export function BackboneRuleList({ rules }: { rules: Rule[] }) {
  if (rules.length === 0) return <p className="py-5 text-sm text-text-primary">No rules yet</p>
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Ref</TableHead>
          <TableHead>Heading</TableHead>
          <TableHead>Kind</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rules.map((rule) => (
          <TableRow key={rule.ref}>
            <TableCell>
              <a href={`/${rule.ref}`} className="underline-offset-2 hover:text-highlight hover:underline">
                {rule.ref}
              </a>
            </TableCell>
            <TableCell>{rule.heading}</TableCell>
            <TableCell>{rule.kind}</TableCell>
            <TableCell>{rule.status}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
