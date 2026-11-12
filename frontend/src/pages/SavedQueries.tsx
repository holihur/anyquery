import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useAuth } from "@/providers/auth"
import { useTenant } from "@/providers/tenant"
import { api } from "@/lib/api"
import { cn, formatDateTime, initials, relativeTime, truncate } from "@/lib/utils"
import type { SavedQuery } from "@/lib/types"
import { Page, PageBody, PageHeader, EmptyState, InlineCode } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Skeleton } from "@/components/ui/skeleton"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Bookmark, Clock3, Globe2, Lock, Play, Plus, Search, Trash2, Users } from "lucide-react"
import { toast } from "sonner"

export function SavedQueriesPage() {
  const { tenant, role } = useTenant()
  const { me } = useAuth()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<"all" | "team" | "private">("all")
  const [deleting, setDeleting] = useState<SavedQuery | null>(null)

  const { data, isLoading } = useQuery({ queryKey: ["saved-queries", tenant.slug], queryFn: () => api.savedQueries(tenant.slug) })
  const { data: tenantInfo } = useQuery({ queryKey: ["tenant", tenant.slug], queryFn: () => api.tenant(tenant.slug) })
  const { data: members } = useQuery({ queryKey: ["members", tenant.slug], queryFn: () => api.members(tenant.slug) })
  const memberById = new Map(members?.members.map((m) => [m.user.id, m.user]) ?? [])

  const deleteMutation = useMutation({
    mutationFn: (q: SavedQuery) => api.deleteSavedQuery(tenant.slug, q.id),
    onSuccess: () => {
      toast.success("Saved query deleted")
      setDeleting(null)
      void queryClient.invalidateQueries({ queryKey: ["saved-queries", tenant.slug] })
      void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not delete query"),
  })

  const queries = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data?.queries ?? []).filter(
      (item) =>
        (filter === "all" || item.visibility === filter) &&
        (!q || item.name.toLowerCase().includes(q) || item.description?.toLowerCase().includes(q) || item.sql.toLowerCase().includes(q)),
    )
  }, [data, query, filter])

  const savedCount = tenantInfo?.usage?.savedQueries ?? data?.queries.length ?? 0
  const savedLimit = tenantInfo?.limits?.maxSavedQueries ?? 100
  const atLimit = savedCount >= savedLimit

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow="Library"
          title={<span className="flex flex-wrap items-center gap-2.5">Saved queries <Badge variant={atLimit ? "warning" : "outline"} className="text-[10px]">{savedCount} / {savedLimit}</Badge></span>}
          description="Reusable analyses shared with your workspace. Open one to run, edit or fork it."
          actions={<Button onClick={() => navigate(`/w/${tenant.slug}/query`)}><Plus className="size-4" /> New query</Button>}
        />

        <div className="mt-5 flex flex-wrap items-center gap-3 rounded-lg bg-muted/25 px-3 py-2 text-[11px] text-muted-foreground">
          <div className="h-1.5 w-40 overflow-hidden rounded-full bg-muted">
            <div
              className={atLimit ? "h-full rounded-full bg-warning transition-all" : "h-full rounded-full bg-primary transition-all"}
              style={{ width: `${Math.min(100, (savedCount / savedLimit) * 100)}%` }}
            />
          </div>
          <span>{savedCount} of {savedLimit} query slots used</span>
          {atLimit && <span className="text-warning-foreground dark:text-warning">Library full — delete a query to save a new one.</span>}
        </div>

        <div className="mt-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="relative w-full sm:max-w-xs">
            <Search className="absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input placeholder="Search saved queries…" value={query} onChange={(e) => setQuery(e.target.value)} className="pl-9" />
          </div>
          <div className="flex items-center gap-1 rounded-lg border bg-card p-0.5">
            {(["all", "team", "private"] as const).map((f) => (
              <button key={f} onClick={() => setFilter(f)} className={cn("rounded-md px-2.5 py-1.5 text-[11px] font-medium capitalize transition-colors", filter === f ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted")}>
                {f}
              </button>
            ))}
          </div>
        </div>

        {isLoading ? (
          <div className="mt-5 grid gap-4 md:grid-cols-2">{Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-[180px] rounded-xl" />)}</div>
        ) : queries.length === 0 ? (
          <EmptyState
            icon={Bookmark}
            title={data?.queries.length ? "No queries match your filters" : "No saved queries yet"}
            description={data?.queries.length ? "Try a different search or visibility filter." : "Save a query from the studio so your whole team can reuse it."}
            className="mt-5"
            action={<Button onClick={() => navigate(`/w/${tenant.slug}/query`)}><Play className="size-4" /> Open query studio</Button>}
          />
        ) : (
          <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {queries.map((q) => {
              const author = memberById.get(q.createdBy)
              const canDelete = role === "owner" || role === "admin" || q.createdBy === me?.user.id
              return (
                <div key={q.id} className="group flex flex-col overflow-hidden surface transition-all hover:shadow-md">
                  <div className="flex items-start gap-2.5 p-4 pb-2">
                    <div className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/8 text-primary">
                      <Bookmark className="size-4" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <h3 className="truncate text-[13.5px] font-semibold">{q.name}</h3>
                      <div className="mt-0.5 flex items-center gap-1.5 text-[10.5px] text-muted-foreground">
                        <span>{relativeTime(q.updatedAt)}</span>
                        <span className="size-0.5 rounded-full bg-current" />
                        <span className="inline-flex items-center gap-1">{q.visibility === "private" ? <Lock className="size-2.5" /> : <Users className="size-2.5" />}{q.visibility}</span>
                      </div>
                    </div>
                  </div>
                  <p className="line-clamp-2 min-h-[38px] px-4 text-[11.5px] leading-relaxed text-muted-foreground">{q.description || "No description"}</p>
                  <div className="mx-4 mt-2 rounded-md border bg-muted/30 p-2.5">
                    <pre className="line-clamp-3 overflow-hidden font-mono text-[10px] leading-relaxed text-muted-foreground">{truncate(q.sql, 220)}</pre>
                  </div>
                  <div className="mt-3 flex items-center gap-2 px-4 text-[10.5px] text-muted-foreground">
                    {author ? (
                      <span className="inline-flex items-center gap-1.5"><Avatar className="size-4"><AvatarFallback className="text-[8px]">{initials(author.name, author.email)}</AvatarFallback></Avatar>{author.name}</span>
                    ) : <span>Teammate</span>}
                    <span className="ml-auto inline-flex items-center gap-1"><Clock3 className="size-2.5" /> {q.lastRunAt ? relativeTime(q.lastRunAt) : "never run"}</span>
                  </div>
                  <div className="mt-3 flex items-center justify-between px-4 py-2.5">
                    <Button variant="ghost" size="xs" asChild><Link to={`/w/${tenant.slug}/query?saved=${q.id}`}><Play className="size-3" /> Open</Link></Button>
                    <div className="flex items-center gap-1">
                      <Badge variant="outline" className="hidden text-[9px] font-mono sm:inline-flex">{formatDateTime(q.updatedAt)}</Badge>
                      {canDelete && (
                        <Button variant="ghost" size="icon-xs" className="text-muted-foreground hover:text-destructive" onClick={() => setDeleting(q)}>
                          <Trash2 className="size-3" />
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        )}

        <div className="mt-6 rounded-xl bg-muted/25 px-4 py-3 text-[11.5px] leading-relaxed text-muted-foreground">
          Tip: reference cross-source tables with <InlineCode>`source`.`table`</InlineCode>. Combine classic SQL with pipeline stages such as <InlineCode>{`|> AGGREGATE …`}</InlineCode>.
        </div>
      </PageBody>

      <AlertDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{deleting?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>This removes the saved query for everyone. Query history is kept.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={() => deleting && deleteMutation.mutate(deleting)}>
              {deleteMutation.isPending ? "Deleting…" : "Delete query"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}
