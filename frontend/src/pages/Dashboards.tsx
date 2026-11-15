import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Clock3, LayoutGrid, Plus, Search, Trash2, Users } from "lucide-react"
import { toast } from "sonner"
import { useTenant } from "@/providers/tenant"
import { api } from "@/lib/api"
import { relativeTime } from "@/lib/utils"
import type { Dashboard } from "@/lib/types"
import { Page, PageBody, PageHeader, EmptyState } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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

export function DashboardsPage() {
  const { tenant, role } = useTenant()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canEdit = role !== "viewer"
  const [search, setSearch] = useState("")
  const [createOpen, setCreateOpen] = useState(false)
  const [deleting, setDeleting] = useState<Dashboard | null>(null)
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")

  const { data, isLoading } = useQuery({
    queryKey: ["dashboards", tenant.slug],
    queryFn: () => api.dashboards(tenant.slug),
  })
  const createMutation = useMutation({
    mutationFn: () => api.createDashboard(tenant.slug, { name, description, panels: [] }),
    onSuccess: (dashboard) => {
      setCreateOpen(false)
      setName("")
      setDescription("")
      void queryClient.invalidateQueries({ queryKey: ["dashboards", tenant.slug] })
      navigate(`/w/${tenant.slug}/dashboards/${dashboard.id}`)
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not create dashboard"),
  })
  const deleteMutation = useMutation({
    mutationFn: (dashboard: Dashboard) => api.deleteDashboard(tenant.slug, dashboard.id),
    onSuccess: () => {
      setDeleting(null)
      toast.success("Dashboard deleted")
      void queryClient.invalidateQueries({ queryKey: ["dashboards", tenant.slug] })
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not delete dashboard"),
  })

  const dashboards = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (data?.dashboards ?? []).filter(
      (dashboard) =>
        !q ||
        dashboard.name.toLowerCase().includes(q) ||
        dashboard.description?.toLowerCase().includes(q) ||
        dashboard.panels.some((panel) => panel.title.toLowerCase().includes(q)),
    )
  }, [data, search])

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow="Workspace"
          title="Dashboards"
          description="Pin query results into editable charts and tables. Panels run against the same federated sources as the query studio."
          actions={
            canEdit ? (
              <Button onClick={() => setCreateOpen(true)}>
                <Plus className="size-4" /> New dashboard
              </Button>
            ) : undefined
          }
        />

        <div className="mt-5 flex items-center justify-between gap-3">
          <div className="relative w-full sm:max-w-xs">
            <Search className="absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search dashboards…" className="pl-9" />
          </div>
          <div className="hidden items-center gap-1.5 text-[11px] text-muted-foreground sm:flex">
            <LayoutGrid className="size-3.5" /> {data?.dashboards.length ?? 0} dashboards
          </div>
        </div>

        {isLoading ? (
          <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-[170px] rounded-xl" />)}
          </div>
        ) : !dashboards.length ? (
          <EmptyState
            icon={LayoutGrid}
            title={data?.dashboards.length ? "No dashboards match your search" : "No dashboards yet"}
            description={data?.dashboards.length ? "Try a different search." : "Create a dashboard, add a panel, and turn a query result into a chart."}
            className="mt-5"
            action={canEdit && !data?.dashboards.length ? <Button onClick={() => setCreateOpen(true)}><Plus className="size-4" /> New dashboard</Button> : undefined}
          />
        ) : (
          <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {dashboards.map((dashboard) => (
              <div key={dashboard.id} className="group surface overflow-hidden transition-all hover:-translate-y-0.5 hover:shadow-md">
                <Link to={`/w/${tenant.slug}/dashboards/${dashboard.id}`} className="block">
                  <div className="flex items-start gap-3 p-4 pb-2">
                    <div className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/8 text-primary">
                      <LayoutGrid className="size-4" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <h2 className="truncate text-[13.5px] font-semibold">{dashboard.name}</h2>
                      <div className="mt-0.5 flex items-center gap-1.5 text-[10.5px] text-muted-foreground">
                        <Clock3 className="size-2.5" /> {relativeTime(dashboard.updatedAt)}
                        <span className="size-0.5 rounded-full bg-current" />
                        <span>{dashboard.panels.length} {dashboard.panels.length === 1 ? "panel" : "panels"}</span>
                      </div>
                    </div>
                  </div>
                  <p className="line-clamp-2 min-h-[34px] px-4 text-[11.5px] leading-relaxed text-muted-foreground">
                    {dashboard.description || "No description"}
                  </p>
                  <div className="mx-4 mt-3 grid grid-cols-4 gap-1 rounded-md border bg-muted/25 p-2">
                    {Array.from({ length: 4 }).map((_, i) => (
                      <div key={i} className={`h-7 rounded-sm ${i < Math.min(4, dashboard.panels.length) ? "bg-primary/18" : "bg-muted/55"}`} />
                    ))}
                  </div>
                </Link>
                <div className="flex items-center justify-between px-4 py-2.5">
                  <Badge variant="outline" className="text-[9px]"><Users className="size-2.5" /> team</Badge>
                  {canEdit && (
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      className="text-muted-foreground hover:text-destructive"
                      onClick={() => setDeleting(dashboard)}
                      aria-label={`Delete ${dashboard.name}`}
                    >
                      <Trash2 className="size-3" />
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </PageBody>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>New dashboard</DialogTitle>
            <DialogDescription>Create a canvas for query-backed panels. You can add charts after creating it.</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="dashboard-name">Name</Label>
              <Input id="dashboard-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Revenue overview" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="dashboard-description">Description</Label>
              <Textarea id="dashboard-description" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this dashboard answers" rows={3} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button disabled={!name.trim() || createMutation.isPending} onClick={() => createMutation.mutate()}>
              {createMutation.isPending ? "Creating…" : "Create dashboard"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{deleting?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>This removes the dashboard and all of its panels for everyone in the workspace.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => deleting && deleteMutation.mutate(deleting)}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete dashboard"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}
