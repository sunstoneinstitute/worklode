import { useNavigate, useParams, useSearch } from "@tanstack/react-router"
import type { UseQueryResult } from "@tanstack/react-query"
import type { ReactNode } from "react"
import { useProjectDocs, useProjectRules } from "@/api/queries"
import { BackboneDocList } from "@/components/BackboneDocList"
import { BackboneRuleList } from "@/components/BackboneRuleList"
import { DocumentList } from "@/components/DocumentList"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DOCUMENT_TABS, type DocumentTab } from "@/lib/content-types"

/**
 * A project's Documents page (WL-REQ-1805): its specs, plans and rules,
 * read-only, and its documents register, one tab each. The tab is `?tab=`.
 */
export function ProjectDocuments() {
  const { projectId } = useParams({ from: "/projects/$projectId/documents" })
  const { tab } = useSearch({ from: "/projects/$projectId/documents" })
  const navigate = useNavigate()

  return (
    <main className="p-5 pt-8 lg:p-page">
      <Tabs
        value={tab}
        onValueChange={(value) => void navigate({ to: ".", search: { tab: value as DocumentTab } })}
      >
        <TabsList>
          {DOCUMENT_TABS.map((option) => (
            <TabsTrigger key={option.value} value={option.value}>
              {option.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="specs">
          <SpecsOrPlans projectId={projectId} kind="spec" />
        </TabsContent>
        <TabsContent value="plans">
          <SpecsOrPlans projectId={projectId} kind="plan" />
        </TabsContent>
        <TabsContent value="rules">
          <Rules projectId={projectId} />
        </TabsContent>
        <TabsContent value="files">
          <DocumentList projectId={projectId} />
        </TabsContent>
      </Tabs>
    </main>
  )
}

function Loaded<T>({ query, noun, children }: { query: UseQueryResult<T>; noun: string; children: (data: T) => ReactNode }) {
  if (query.isPending) return <LoadingBlock label={`Loading ${noun}`} className="h-64" />
  if (query.isError) return <LoadError message={`Couldn't load ${noun}.`} onRetry={() => void query.refetch()} />
  return children(query.data)
}

function SpecsOrPlans({ projectId, kind }: { projectId: string; kind: "spec" | "plan" }) {
  const docs = useProjectDocs(projectId, kind)
  return (
    <Loaded query={docs} noun={`${kind}s`}>
      {(data) => <BackboneDocList docs={data} empty={kind === "spec" ? "No specs yet" : "No plans yet"} />}
    </Loaded>
  )
}

function Rules({ projectId }: { projectId: string }) {
  const rules = useProjectRules(projectId)
  return <Loaded query={rules} noun="rules">{(data) => <BackboneRuleList rules={data} />}</Loaded>
}
