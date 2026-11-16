import { useEffect, useState } from "react"
import { BarChart3, Eye, Loader2 } from "lucide-react"
import { api } from "@/lib/api"
import type { DashboardPanel, DashboardVisualization, QueryResult } from "@/lib/types"
import { ChartRenderer, VisualizationBuilder } from "@/components/query/ChartRenderer"
import { ErrorNotice } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

function newPanel(): DashboardPanel {
  return {
    id: `p-${Date.now().toString(36)}`,
    title: "New panel",
    sql: "",
    visualization: { type: "bar", x: "", y: [] },
    layout: { x: 0, y: 0, w: 6, h: 4 },
  }
}

export function PanelEditor({
  slug,
  open,
  panel,
  onOpenChange,
  onSave,
}: {
  slug: string
  open: boolean
  panel: DashboardPanel | null
  onOpenChange: (open: boolean) => void
  onSave: (panel: DashboardPanel) => void
}) {
  const [draft, setDraft] = useState<DashboardPanel>(() => panel ?? newPanel())
  const [preview, setPreview] = useState<QueryResult | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [previewError, setPreviewError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setDraft(panel ?? newPanel())
    setPreview(null)
    setPreviewError(null)
    setPreviewing(false)
  }, [open, panel])

  async function runPreview() {
    if (!draft.sql.trim()) return
    setPreviewing(true)
    setPreviewError(null)
    try {
      const payload = await api.execute(slug, draft.sql)
      if (payload.error) {
        setPreview(null)
        setPreviewError(payload.error.message)
      } else {
        const result = payload.result ?? null
        setPreview(result)
        if (result?.columns.length) {
          const numeric = result.columns.filter((c) => c.type === "integer" || c.type === "number").map((c) => c.name)
          const category = result.columns.find((c) => !numeric.includes(c.name))?.name ?? result.columns[0]?.name ?? ""
          setDraft((current) => {
            if (current.visualization.x || current.visualization.y?.length) return current
            return {
              ...current,
              visualization: {
                ...current.visualization,
                x: category,
                y: numeric.filter((name) => name !== category).slice(0, 3),
              },
            }
          })
        }
      }
    } catch (err) {
      setPreviewError(err instanceof Error ? err.message : "Preview failed")
    } finally {
      setPreviewing(false)
    }
  }

  function setVisualization(config: DashboardVisualization) {
    setDraft((current) => ({ ...current, visualization: config }))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{panel ? "Edit panel" : "Add panel"}</DialogTitle>
          <DialogDescription>Each panel runs its own read-only SQL query. Preview it, then choose how the result is rendered.</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="panel-title">Title</Label>
            <Input id="panel-title" value={draft.title} onChange={(e) => setDraft({ ...draft, title: e.target.value })} placeholder="Revenue by region" />
          </div>
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <Label htmlFor="panel-sql">SQL</Label>
              <Button type="button" variant="outline" size="xs" onClick={() => void runPreview()} disabled={!draft.sql.trim() || previewing}>
                {previewing ? <Loader2 className="size-3 animate-spin" /> : <Eye className="size-3" />} Preview
              </Button>
            </div>
            <Textarea
              id="panel-sql"
              value={draft.sql}
              onChange={(e) => {
                setDraft({ ...draft, sql: e.target.value })
                setPreview(null)
                setPreviewError(null)
              }}
              placeholder="SELECT region, sum(total) AS revenue FROM mongo.orders GROUP BY region"
              className="min-h-[120px] font-mono text-[12px]"
              spellCheck={false}
            />
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="panel-width">Width</Label>
              <Select value={String(draft.layout.w)} onValueChange={(value) => setDraft({ ...draft, layout: { ...draft.layout, w: Number(value) } })}>
                <SelectTrigger id="panel-width"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="3">1/4 width</SelectItem>
                  <SelectItem value="4">1/3 width</SelectItem>
                  <SelectItem value="6">1/2 width</SelectItem>
                  <SelectItem value="8">2/3 width</SelectItem>
                  <SelectItem value="12">Full width</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="panel-height">Height</Label>
              <Select value={String(draft.layout.h)} onValueChange={(value) => setDraft({ ...draft, layout: { ...draft.layout, h: Number(value) } })}>
                <SelectTrigger id="panel-height"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="3">Compact</SelectItem>
                  <SelectItem value="4">Standard</SelectItem>
                  <SelectItem value="6">Tall</SelectItem>
                  <SelectItem value="8">Extra tall</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {preview ? (
            <VisualizationBuilder columns={preview.columns} config={draft.visualization} onChange={setVisualization} />
          ) : (
            <div className="flex items-center gap-2 rounded-lg border border-dashed bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
              Run Preview to load result columns, then configure the visualization.
            </div>
          )}

          {previewError && <ErrorNotice message={previewError} />}
          {preview && (
            <div className="h-[280px] rounded-lg border bg-card p-3">
              {preview.rows.length ? (
                <ChartRenderer result={preview} config={draft.visualization} />
              ) : (
                <div className="grid h-full place-items-center text-[12px] text-muted-foreground">Query returned no rows.</div>
              )}
            </div>
          )}
          {!preview && (
            <div className="flex items-center gap-2 rounded-lg border border-dashed bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
              <BarChart3 className="size-3.5" /> Run Preview to load result columns and see the chart.
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            disabled={!draft.sql.trim()}
            onClick={() => {
              const next = {
                ...draft,
                title: draft.title.trim() || "Panel",
                sql: draft.sql.trim(),
              }
              onSave(next)
              onOpenChange(false)
            }}
          >
            {panel ? "Save panel" : "Add panel"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
