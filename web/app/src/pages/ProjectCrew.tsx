import { type FormEvent, useState } from "react"
import { useParams } from "@tanstack/react-router"
import { useAddCrewMember, useRemoveCrewMember } from "@/api/mutations"
import { useCrew, useProjectCrew } from "@/api/queries"
import { TrashIcon } from "@/components/icons"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn } from "@/components/ui/data-table"
import { Input } from "@/components/ui/input"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

type Row = { actor: string; name: string; roles: string; lead: boolean }

function getRowId(row: Row) {
  return row.actor
}

/** A project's Crew roster: who is on it, and adding and removing members (WL-REQ-1802) */
export function ProjectCrew() {
  const { projectId } = useParams({ from: "/projects/$projectId/crew" })
  const roster = useProjectCrew(projectId)
  const remove = useRemoveCrewMember(projectId)

  const columns: DataTableColumn<Row>[] = [
    { key: "name", label: "Name", type: "text", width: "w-70", filterable: false },
    { key: "roles", label: "Roles", type: "text", filterable: false },
    {
      key: "lead",
      label: "Lead",
      type: "custom",
      width: "w-30",
      sortable: false,
      filterable: false,
      render: (row) => (row.lead ? <Badge>Lead</Badge> : null),
    },
    {
      key: "actions",
      label: "",
      type: "actions",
      actions: [
        {
          id: "remove",
          label: "Remove",
          icon: <TrashIcon size={18} />,
          variant: "destructive",
          confirm: (row) => `Remove ${row.name} from the Crew?`,
          when: (row) => !row.lead,
          onSelect: (row) => remove.mutate(row.actor),
        },
      ],
    },
  ]

  let content
  if (roster.isPending) content = <LoadingBlock label="Loading crew" className="h-64" />
  else if (roster.isError)
    content = <LoadError message="Couldn't load the Crew." onRetry={() => void roster.refetch()} />
  else {
    const rows: Row[] = roster.data.map((m) => ({
      actor: m.actor ?? "",
      name: m.display_name || (m.actor ?? ""),
      roles: (m.roles ?? []).join(", "),
      lead: m.lead ?? false,
    }))
    content = (
      <>
        {remove.isError && (
          <p role="alert" className="mb-5 rounded-card bg-surface p-5 text-sm text-destructive">
            {remove.error.message}
          </p>
        )}
        <DataTable
          // The table copies `data` when it mounts; a new key per fetch shows a refetch
          key={roster.dataUpdatedAt}
          columns={columns}
          data={rows}
          getRowId={getRowId}
          title="Crew"
          toolbarActions={<AddCrewMember projectId={projectId} members={rows.map((r) => r.actor)} />}
        />
        {rows.length === 0 && <p className="p-5 text-sm text-text-primary">No Crew yet</p>}
      </>
    )
  }

  return <main className="p-5 pt-8 lg:p-page">{content}</main>
}

function AddCrewMember({ projectId, members }: { projectId: string; members: string[] }) {
  const crew = useCrew()
  const add = useAddCrewMember(projectId)
  const [actor, setActor] = useState("")
  const [role, setRole] = useState("")
  const [lead, setLead] = useState(false)
  const candidates = (crew.data ?? []).filter((c) => c.actor && !members.includes(c.actor))

  function submit(event: FormEvent) {
    event.preventDefault()
    add.mutate(
      { actor, role, lead },
      {
        onSuccess: () => {
          setActor("")
          setRole("")
          setLead(false)
        },
      },
    )
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-center gap-2.5">
      <Select value={actor} onValueChange={setActor}>
        <SelectTrigger aria-label="Person" className="w-48">
          <SelectValue placeholder="Person" />
        </SelectTrigger>
        <SelectContent>
          {candidates.map((c) => (
            <SelectItem key={c.actor} value={c.actor ?? ""}>
              {c.display_name || c.actor}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Input aria-label="Role" placeholder="member" value={role} onChange={(e) => setRole(e.target.value)} className="w-36" />
      <label className="flex items-center gap-1.5 text-sm text-text-primary">
        <input type="checkbox" checked={lead} onChange={(e) => setLead(e.target.checked)} />
        Lead
      </label>
      <Button type="submit" size="sm" disabled={!actor || add.isPending}>
        Add
      </Button>
      {add.isError && (
        <span role="alert" className="text-sm text-destructive">
          {add.error.message}
        </span>
      )}
    </form>
  )
}
