import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeft,
  Check,
  Clock3,
  LayoutGrid,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Settings2,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"
import { useTenant } from "@/providers/tenant"
import { api, ApiError } from "@/lib/api"
import { formatDateTime, relativeTime } from "@/lib/utils"
import type { Dashboard, DashboardPanel } from "@/lib/types"
import { Page, PageBody, PageHeader, EmptyState, LoadingBlock } from "@/components/shared"
import { DashboardPanelCard } from "@/components/dashboard/DashboardPanelCard"
import { PanelEditor } from "@/components/dashboard/PanelEditor"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
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

const REFRESH_OPTIONS = [
  { value: 0, label: "Off" },
  { value: 15_000, label: "15s" },
  { value: 30_000, label: "30s" },
  { value: 60_000, label: "1m" },
  { value: 300_000, label: "5m" },
]

function clampSpan(value: number, fallback: number) {
  if (!Number.isFinite(value) || value <= 0) return fallback
  return Math.min(12, Math.max(1, Math.round(value)))
}

export function DashboardPage() {
  const { tenant, role } = useTenant()
  const { id = "" } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canEdit = role !== "viewer"
  const [dashboard, setDashboard] = useState<Dashboard | null>(null)
  const [editMode, setEditMode] = useState(false)
  const [panelOpen, setPanelOpen] = useState(false)
  const [editingPanel, setEditingPanel] = useState<DashboardPanel | null>(null)
  const [deletingPanel, setDeletingPanel] = useState<DashboardPanel | null>(null)
  const [dragged, setDragged] = useState<string | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsName, setSettingsName] = useState("")
  const [settingsDescription, setSettingsDescription] = useState("")
  const [refreshMs, setRefreshMs] = useState(0)

  const { data, isLoading, error } = useQuery({
    queryKey: ["dashboard", tenant.slug, id],
    queryFn: () => api.dashboard(tenant.slug, id),
    enabled: Boolean(id),
  })

  useEffect(() => {
    if (!data) return
    setDashboard(data)
    setSettingsName(data.name)
    setSettingsDescription(data.description ?? "")
  }, [data])

  const panelsKey = dashboard?.panels.map((panel) => `${panel.id}\x00${panel.sql}`).join("\x01") ?? ""
  const run = useQuery({
    queryKey: ["dashboard-run", tenant.slug, id, panelsKey],
    queryFn: async () => {
      try {
        return await api.runDashboard(tenant.slug, id)
      } catch (err) {
        // Rolling upgrades: an older backend may not have the batch endpoint.
        if (!(err instanceof ApiError) || err.status !== 404 || !dashboard) throw err
        const panels = await Promise.all(dashboard.panels.map(async (panel) => {
          const payload = await api.execute(tenant.slug, panel.sql)
          return {
            id: panel.id,
            result: payload.result,
            error: payload.error,
            durationMs: payload.run?.durationMs ?? 0,
            cacheHit: payload.run?.cacheHit,
          }
        }))
        return { dashboardId: id, panels }
      }
    },
    enabled: Boolean(id && dashboard && dashboard.panels.length > 0),
    retry: false,
    staleTime: 0,
    refetchInterval: refreshMs > 0 ? refreshMs : false,
  })
  const panelRuns = new Map((run.data?.panels ?? []).map((panel) => [panel.id, panel]))

  const saveMutation = useMutation({
    mutationFn: (next: Dashboard) =>
      api.updateDashboard(tenant.slug, next.id, { name: next.name, description: next.description, panels: next.panels }),
    onSuccess: (updated) => {
      setDashboard(updated)
      queryClient.setQueryData(["dashboard", tenant.slug, id], updated)
      queryClient.setQueryData(["dashboards", tenant.slug], (old: { dashboards?: Dashboard[] } | undefined) => {
        if (!old?.dashboards) return old
        return { dashboards: old.dashboards.map((item) => (item.id === updated.id ? updated : item)) }
      })
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not save dashboard"),
  })

  function commit(next: Dashboard) {
    setDashboard(next)
    saveMutation.mutate(next)
  }

  function openAddPanel() {
    setEditingPanel(null)
    setPanelOpen(true)
  }

  function openEditPanel(panel: DashboardPanel) {
    setEditingPanel(panel)
    setPanelOpen(true)
  }

  function savePanel(panel: DashboardPanel) {
    if (!dashboard) return
    const panels = [...dashboard.panels]
    const index = panels.findIndex((item) => item.id === panel.id)
    if (index >= 0) panels[index] = panel
    else panels.push(panel)
    commit({ ...dashboard, panels })
    toast.success(index >= 0 ? "Panel updated" : "Panel added")
  }

  function deletePanel() {
    if (!dashboard || !deletingPanel) return
    commit({ ...dashboard, panels: dashboard.panels.filter((panel) => panel.id !== deletingPanel.id) })
    setDeletingPanel(null)
    toast.success("Panel removed")
  }

  function dropPanel(targetID: string) {
    if (!dashboard || !dragged || dragged === targetID) return
    const panels = [...dashboard.panels]
    const from = panels.findIndex((panel) => panel.id === dragged)
    const to = panels.findIndex((panel) => panel.id === targetID)
    if (from < 0 || to < 0) return
    const [moved] = panels.splice(from, 1)
    panels.splice(to, 0, moved)
    commit({ ...dashboard, panels })
    setDragged(null)
  }

  function saveSettings() {
    if (!dashboard || !settingsName.trim()) return
    commit({ ...dashboard, name: settingsName.trim(), description: settingsDescription.trim() })
    setSettingsOpen(false)
    toast.success("Dashboard saved")
  }

  if (isLoading) return <Page><PageBody><LoadingBlock label="Loading dashboard…" /></PageBody></Page>
  if (error || !dashboard) {
    const message = error instanceof ApiError ? error.message : "Dashboard not found"
    return (
      <Page><PageBody>
        <EmptyState
          icon={LayoutGrid}
          title="Dashboard not found"
          description={message}
          action={<Button asChild><Link to={`/w/${tenant.slug}/dashboards`}><ArrowLeft className="size-4" /> Back to dashboards</Link></Button>}
        />
      </PageBody></Page>
    )
  }

  return (
    <Page>
      <PageBody>
        <Link to={`/w/${tenant.slug}/dashboards`} className="mb-4 inline-flex items-center gap-1.5 text-[11.5px] text-muted-foreground hover:text-foreground">
          <ArrowLeft className="size-3.5" /> Dashboards
        </Link>

        <PageHeader
          eyebrow={<span className="inline-flex items-center gap-1.5"><LayoutGrid className="size-3" /> Dashboard</span>}
          title={dashboard.name}
          description={dashboard.description || "A live view of query-backed panels."}
          actions={
            <>
              <Select value={String(refreshMs)} onValueChange={(value) => setRefreshMs(Number(value))}>
                <SelectTrigger size="sm" className="h-8 w-[104px] text-[11px]"><RefreshCw className="size-3.5" /><SelectValue /></SelectTrigger>
                <SelectContent>{REFRESH_OPTIONS.map((option) => <SelectItem key={option.value} value={String(option.value)}>{option.label}</SelectItem>)}</SelectContent>
              </Select>
              <Button variant="outline" size="sm" onClick={() => void run.refetch()} disabled={run.isFetching}>
                <RefreshCw className={run.isFetching ? "size-3.5 animate-spin" : "size-3.5"} /> Refresh
              </Button>
              {canEdit && (
                <Button variant="outline" size="sm" onClick={() => setSettingsOpen(true)}>
                  <Settings2 className="size-3.5" /> Settings
                </Button>
              )}
              {canEdit && (
                <Button variant={editMode ? "default" : "outline"} size="sm" onClick={() => setEditMode((value) => !value)}>
                  {editMode ? <Check className="size-3.5" /> : <Pencil className="size-3.5" />} {editMode ? "Done" : "Edit"}
                </Button>
              )}
              {canEdit && editMode && (
                <Button size="sm" onClick={openAddPanel}><Plus className="size-4" /> Add panel</Button>
              )}
            </>
          }
        />

        <div className="mt-5 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <Badge variant="outline">{dashboard.panels.length} {dashboard.panels.length === 1 ? "panel" : "panels"}</Badge>
          <span className="inline-flex items-center gap-1"><Clock3 className="size-3" /> updated {relativeTime(dashboard.updatedAt)}</span>
          {saveMutation.isPending && <span className="inline-flex items-center gap-1 text-primary"><Loader2 className="size-3 animate-spin" /> saving…</span>}
          {saveMutation.isError && <span className="text-destructive">Last change was not saved</span>}
          <span className="ml-auto hidden sm:inline">{formatDateTime(dashboard.updatedAt)}</span>
        </div>

        {dashboard.panels.length === 0 ? (
          <EmptyState
            icon={LayoutGrid}
            title="This dashboard is empty"
            description="Add a panel with SQL, then choose a chart or table visualization."
            className="mt-5"
            action={canEdit ? <Button onClick={openAddPanel}><Plus className="size-4" /> Add first panel</Button> : undefined}
          />
        ) : (
          <div className="mt-4 grid grid-cols-12 auto-rows-[42px] gap-3" style={{ gridAutoFlow: "row dense" }}>
            {dashboard.panels.map((panel) => (
              <div
                key={panel.id}
                style={{
                  gridColumn: `span ${clampSpan(panel.layout.w, 6)}`,
                  gridRow: `span ${clampSpan(panel.layout.h, 4)}`,
                }}
                className="min-h-0"
              >
                <DashboardPanelCard
                  slug={tenant.slug}
                  panel={panel}
                  editMode={editMode}
                  result={panelRuns.get(panel.id)?.result}
                  error={panelRuns.get(panel.id)?.error?.message || (run.error instanceof Error ? run.error.message : null)}
                  loading={run.isLoading && !run.data}
                  refreshing={run.isFetching}
                  cacheHit={panelRuns.get(panel.id)?.cacheHit}
                  onRefresh={() => void run.refetch()}
                  onEdit={() => openEditPanel(panel)}
                  onDelete={() => setDeletingPanel(panel)}
                  onDragStart={() => setDragged(panel.id)}
                  onDrop={() => dropPanel(panel.id)}
                />
              </div>
            ))}
          </div>
        )}

        {canEdit && !editMode && dashboard.panels.length > 0 && (
          <div className="mt-4 flex items-center gap-2 text-[11px] text-muted-foreground">
            <Pencil className="size-3" /> Use Edit to add, resize, reorder or delete panels.
          </div>
        )}
      </PageBody>

      <PanelEditor
        slug={tenant.slug}
        open={panelOpen}
        panel={editingPanel}
        onOpenChange={setPanelOpen}
        onSave={savePanel}
      />

      <AlertDialog open={Boolean(deletingPanel)} onOpenChange={(open) => !open && setDeletingPanel(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove “{deletingPanel?.title}”?</AlertDialogTitle>
            <AlertDialogDescription>This panel will be removed from the dashboard. The underlying query remains available.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={deletePanel}>Remove panel</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={settingsOpen} onOpenChange={setSettingsOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Dashboard settings</DialogTitle>
            <DialogDescription>Rename the dashboard or update its description.</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5"><Label htmlFor="dashboard-rename">Name</Label><Input id="dashboard-rename" value={settingsName} onChange={(e) => setSettingsName(e.target.value)} /></div>
            <div className="space-y-1.5"><Label htmlFor="dashboard-description-edit">Description</Label><Textarea id="dashboard-description-edit" value={settingsDescription} onChange={(e) => setSettingsDescription(e.target.value)} rows={3} /></div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setSettingsOpen(false)}>Cancel</Button>
            <Button disabled={!settingsName.trim() || saveMutation.isPending} onClick={saveSettings}>Save changes</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Page>
  )
}
