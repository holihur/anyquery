import { ExternalLink, GripVertical, Loader2, Pencil, RefreshCw, TriangleAlert, Trash2 } from "lucide-react"
import { Link } from "react-router-dom"
import { cn, formatDuration, formatNumber, truncate } from "@/lib/utils"
import type { DashboardPanel, QueryResult } from "@/lib/types"
import { ChartRenderer, ChartTypeLabel } from "@/components/query/ChartRenderer"
import { Button } from "@/components/ui/button"

export function DashboardPanelCard({
  slug,
  panel,
  editMode,
  result,
  error,
  loading,
  refreshing,
  cacheHit,
  onRefresh,
  onEdit,
  onDelete,
  onDragStart,
  onDrop,
}: {
  slug: string
  panel: DashboardPanel
  editMode: boolean
  result?: QueryResult
  error?: string | null
  loading: boolean
  refreshing: boolean
  cacheHit?: boolean
  onRefresh: () => void
  onEdit: () => void
  onDelete: () => void
  onDragStart: () => void
  onDrop: () => void
}) {

  return (
    <article
      draggable={editMode}
      onDragStart={(event) => {
        if (!editMode) return
        event.dataTransfer.effectAllowed = "move"
        event.dataTransfer.setData("text/plain", panel.id)
        onDragStart()
      }}
      onDragOver={(event) => {
        if (editMode) event.preventDefault()
      }}
      onDrop={(event) => {
        if (!editMode) return
        event.preventDefault()
        onDrop()
      }}
      className={cn(
        "group flex h-full min-h-0 flex-col overflow-hidden rounded-xl border bg-card transition-shadow",
        editMode && "ring-1 ring-primary/10 hover:ring-primary/35",
      )}
    >
      <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border/50 px-3">
        {editMode && <GripVertical className="size-3.5 shrink-0 cursor-grab text-muted-foreground/60" />}
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-[12px] font-semibold">{panel.title || "Untitled panel"}</h3>
        </div>
        <span className="hidden shrink-0 rounded border bg-muted/40 px-1.5 py-0.5 text-[9px] text-muted-foreground sm:inline">{ChartTypeLabel(panel.visualization.type)}</span>
        <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <Button variant="ghost" size="icon-xs" onClick={onRefresh} title="Refresh dashboard">
            {refreshing ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
          </Button>
          {editMode && (
            <>
              <Button variant="ghost" size="icon-xs" onClick={onEdit} title="Edit panel"><Pencil className="size-3" /></Button>
              <Button variant="ghost" size="icon-xs" className="text-muted-foreground hover:text-destructive" onClick={onDelete} title="Delete panel"><Trash2 className="size-3" /></Button>
            </>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1 p-2.5">
        {loading ? (
          <div className="grid h-full place-items-center"><Loader2 className="size-4 animate-spin text-muted-foreground" /></div>
        ) : error ? (
          <div className="flex h-full flex-col justify-center rounded-md bg-destructive/5 px-3 text-[11px] text-destructive">
            <div className="flex items-center gap-1.5 font-medium"><TriangleAlert className="size-3.5" /> Panel query failed</div>
            <div className="mt-1 line-clamp-3 text-destructive/75">{error}</div>
          </div>
        ) : result ? (
          <ChartRenderer result={result} config={panel.visualization} />
        ) : (
          <div className="grid h-full place-items-center text-[11px] text-muted-foreground">No result</div>
        )}
      </div>

      <footer className="flex h-7 shrink-0 items-center gap-2 border-t border-border/40 px-3 text-[9.5px] text-muted-foreground">
        {result ? (
          <>
            <span>{formatNumber(result.rowCount)} rows</span>
            <span className="size-0.5 rounded-full bg-current" />
            <span>{formatDuration(result.stats.durationMs)}</span>
            {cacheHit && <span className="rounded border bg-muted/50 px-1 text-[8.5px]">cached</span>}
            {result.warnings?.length ? <span className="ml-auto text-warning">truncated</span> : null}
          </>
        ) : <span className="truncate">{truncate(panel.sql.replace(/\s+/g, " "), 90)}</span>}
        <Button variant="ghost" size="icon-xs" className="ml-auto size-5 opacity-0 group-hover:opacity-100" asChild>
          <Link to={`/w/${slug}/query?sql=${encodeURIComponent(panel.sql)}`} title="Open SQL in query studio"><ExternalLink className="size-2.5" /></Link>
        </Button>
      </footer>
    </article>
  )
}
