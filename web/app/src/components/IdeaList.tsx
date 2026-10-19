import { useCallback, useMemo } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { useDeleteIdea, useShareIdea } from "@/api/mutations"
import { useCrew, useIdeas, useMe } from "@/api/queries"
import type { Idea } from "@/api/types"
import { ForwardIcon, PencilIcon, TrashIcon } from "@/components/icons"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DataTable,
  type DataTableColumn,
  type DataTableUser,
  type SelectOption,
  badgeStyle,
  formatIsoDate,
  userHandle,
} from "@/components/ui/data-table"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { useTableUrlState } from "@/hooks/use-table-url-state"
import { IDEA_STATUS_OPTIONS, IDEA_TYPE_OPTIONS, STORY_TYPE_OPTIONS } from "@/lib/content-types"

/** The share confirm's question (WL-REQ-1806): sharing cannot be undone */
export const SHARE_IDEA_CONFIRM =
  "Share this idea? Everyone will be able to see it in intake, and it cannot be made private again."

function ReadOnlyBadge({ value, options }: { value: string; options: SelectOption[] }) {
  if (!value) return null
  const option = options.find((o) => o.value === value)
  const { className, style } = badgeStyle(option)
  return (
    <Badge className={className} style={style}>
      {option?.label ?? value}
    </Badge>
  )
}

// Every cell is read-only: an idea changes through its form, and leaves
// private only through Share (WL-REQ-1799)
function ideaColumns(me: string, users: DataTableUser[], onEdit: (id: string) => void): DataTableColumn<Idea>[] {
  const badge = (key: keyof Idea, options: SelectOption[]) => (idea: Idea) => (
    <ReadOnlyBadge value={String(idea[key] ?? "")} options={options} />
  )
  return [
    {
      key: "title",
      label: "Title",
      type: "text",
      width: "w-70",
      filterable: false,
      // Every visible idea has an Intake page (WL-REQ-1807)
      render: (idea) => (
        <Link
          to="/ideas/$ideaId/intake"
          params={{ ideaId: idea.id ?? "" }}
          className="underline-offset-2 transition-colors hover:text-highlight hover:underline"
        >
          {idea.title}
        </Link>
      ),
    },
    {
      key: "author",
      label: "Author",
      type: "user",
      width: "w-50",
      render: (idea) => {
        const user = users.find((u) => u.id === idea.author)
        return user ? userHandle(user) : idea.author
      },
    },
    { key: "type", label: "Type", type: "select", width: "w-40", options: IDEA_TYPE_OPTIONS, render: badge("type", IDEA_TYPE_OPTIONS) },
    {
      key: "story_type",
      label: "Story Type",
      type: "select",
      width: "w-40",
      options: STORY_TYPE_OPTIONS,
      render: badge("story_type", STORY_TYPE_OPTIONS),
    },
    {
      key: "status",
      label: "Status",
      type: "select",
      width: "w-40",
      options: IDEA_STATUS_OPTIONS,
      render: badge("status", IDEA_STATUS_OPTIONS),
    },
    { key: "submitted", label: "Submitted", type: "date", width: "w-40", render: (idea) => formatIsoDate(idea.submitted ?? "") },
    {
      key: "actions",
      label: "",
      type: "actions",
      actions: [
        {
          id: "share",
          label: "Share idea",
          icon: <ForwardIcon size={20} />,
          confirm: SHARE_IDEA_CONFIRM,
          when: (idea) => idea.status === "private" && idea.author === me,
          onSelect: (_idea, { updateRow }) => updateRow({ status: "intake" }),
        },
        {
          id: "edit",
          label: "Edit idea",
          icon: <PencilIcon size={20} />,
          when: (idea) => idea.author === me,
          onSelect: (idea) => onEdit(idea.id ?? ""),
        },
        {
          id: "delete",
          label: "Delete idea",
          icon: <TrashIcon size={20} />,
          variant: "destructive",
          confirm: (idea) => `Delete ${idea.title}?`,
          onSelect: (_idea, { removeRow }) => removeRow(),
        },
      ],
    },
  ]
}

function getIdeaId(idea: Idea) {
  return idea.id ?? ""
}

/**
 * The Ideas table (WL-REQ-1806): the caller's private ideas and every shared
 * one. Share is the row action that sets status to intake, sent as
 * `POST /share`, never as a field edit.
 */
export function IdeaList() {
  const me = useMe().id ?? ""
  const ideas = useIdeas()
  const crew = useCrew()
  const navigate = useNavigate()
  const shareMutate = useShareIdea().mutate
  const deleteMutate = useDeleteIdea().mutate
  const users = useMemo<DataTableUser[]>(
    () => (crew.data ?? []).map((c) => ({ id: c.actor ?? "", name: c.display_name || (c.actor ?? "") })),
    [crew.data],
  )
  const columns = useMemo(
    () => ideaColumns(me, users, (ideaId) => void navigate({ to: "/ideas/$ideaId/edit", params: { ideaId } })),
    [me, users, navigate],
  )
  const urlState = useTableUrlState(columns)
  const onUpdateRow = useCallback(
    (id: string, patch: Partial<Idea>) => {
      if (patch.status === "intake") shareMutate(id)
    },
    [shareMutate],
  )
  const onRemoveRow = useCallback((id: string) => deleteMutate(id), [deleteMutate])

  if (ideas.isPending) return <LoadingBlock label="Loading ideas" className="h-80" />
  if (ideas.isError) return <LoadError message="Couldn't load ideas." onRetry={() => void ideas.refetch()} />

  return (
    <div className="flex flex-col gap-4">
      <DataTable
        columns={columns}
        data={ideas.data}
        getRowId={getIdeaId}
        users={users}
        onUpdateRow={onUpdateRow}
        onRemoveRow={onRemoveRow}
        title="Ideas"
        {...urlState}
        toolbarActions={
          <Button asChild variant="outline" size="sm" className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark">
            <Link to="/ideas/new">New idea</Link>
          </Button>
        }
      />
      <p className="text-sm text-text-primary">
        Your ideas are private: only you can see them. Share one to send it to intake, where everyone can see it.
      </p>
    </div>
  )
}
