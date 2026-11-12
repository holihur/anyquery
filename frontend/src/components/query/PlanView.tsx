import {
  ArrowDownUp,
  Boxes,
  Braces,
  CircleAlert,
  Columns3,
  Combine,
  Database,
  Filter,
  GitMerge,
  Layers,
  ListFilter,
  Loader2,
  PanelsTopLeft,
  RefreshCw,
  Rows3,
  Sigma,
  Table2,
  type LucideIcon,
} from "lucide-react"
import { cn, formatNumber } from "@/lib/utils"
import type { PlanNode } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ErrorNotice } from "@/components/shared"

const operatorMeta: Record<string, { icon: LucideIcon; color: string; bg: string }> = {
  "Source Scan": { icon: Database, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10" },
  Filter: { icon: Filter, color: "text-amber-600 dark:text-amber-400", bg: "bg-amber-500/10" },
  Aggregate: { icon: Sigma, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10" },
  Having: { icon: Filter, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10" },
  "Hash Join": { icon: GitMerge, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10" },
  "Nested Loop Join": { icon: GitMerge, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10" },
  "Cross Join": { icon: GitMerge, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10" },
  Project: { icon: Columns3, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10" },
  Window: { icon: PanelsTopLeft, color: "text-fuchsia-600 dark:text-fuchsia-400", bg: "bg-fuchsia-500/10" },
  Sort: { icon: ArrowDownUp, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10" },
  Limit: { icon: ListFilter, color: "text-rose-600 dark:text-rose-400", bg: "bg-rose-500/10" },
  Distinct: { icon: Rows3, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10" },
  Union: { icon: Combine, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10" },
  Intersect: { icon: Combine, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10" },
  Except: { icon: Combine, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10" },
  "CTE Scan": { icon: Boxes, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10" },
  Subquery: { icon: Layers, color: "text-slate-600 dark:text-slate-400", bg: "bg-slate-500/10" },
  Values: { icon: Table2, color: "text-muted-foreground", bg: "bg-muted" },
}

function metaFor(operator: string) {
  return operatorMeta[operator] ?? { icon: Braces, color: "text-muted-foreground", bg: "bg-muted" }
}

function propertyEntries(node: PlanNode) {
  const props = node.properties ?? {}
  const skip = new Set(["operation", "algorithm", "natural", "ctes"])
  return Object.entries(props).filter(([key, value]) => !skip.has(key) && value !== undefined && value !== null && value !== "").slice(0, 6)
}

function propertyText(value: unknown) {
  if (Array.isArray(value)) return value.join(", ")
  if (typeof value === "object") return JSON.stringify(value)
  return String(value)
}

function PlanNodeCard({ node, depth }: { node: PlanNode; depth: number }) {
  const meta = metaFor(node.operator)
  const Icon = meta.icon
  const entries = propertyEntries(node)
  return (
    <div className="relative">
      <div className={cn("relative flex items-start gap-3 rounded-lg border border-border/60 bg-card p-3", depth === 0 && "border-primary/25 shadow-sm")}>
        <div className={cn("grid size-8 shrink-0 place-items-center rounded-md", meta.bg, meta.color)}>
          <Icon className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[12.5px] font-semibold">{node.operator}</span>
            {typeof node.properties?.algorithm === "string" && <Badge variant="outline" className="px-1 py-0 text-[8.5px] font-mono">{String(node.properties.algorithm)}</Badge>}
            {node.estimatedRows ? <Badge variant="muted" className="px-1 py-0 text-[8.5px]">{formatNumber(node.estimatedRows)} est. rows</Badge> : null}
          </div>
          {node.detail && <div className="mt-1 break-words font-mono text-[10.5px] leading-relaxed text-muted-foreground">{node.detail}</div>}
          {entries.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {entries.map(([key, value]) => (
                <span key={key} className="inline-flex max-w-full items-center gap-1 rounded bg-muted/60 px-1.5 py-0.5 font-mono text-[9px] text-muted-foreground">
                  <span className="opacity-60">{key}</span>
                  <span className="truncate text-foreground/75">{propertyText(value)}</span>
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
      {node.children && node.children.length > 0 && (
        <div className="mt-2 ml-4 space-y-2 border-l border-border/55 pl-4">
          {node.children.map((child) => (
            <div key={child.id} className="relative before:absolute before:top-5 before:-left-4 before:h-px before:w-4 before:bg-border/55">
              <PlanNodeCard node={child} depth={depth + 1} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function collectStats(node: PlanNode) {
  let operators = 0
  const sources = new Set<string>()
  const scans: string[] = []
  const walk = (n: PlanNode) => {
    operators++
    if (n.operator === "Source Scan") {
      const source = String(n.properties?.source ?? "")
      const table = String(n.properties?.table ?? "")
      if (source) sources.add(source)
      if (source && table) scans.push(`${source}.${table}`)
    }
    n.children?.forEach(walk)
  }
  walk(node)
  return { operators, sources: sources.size, scans }
}

export function PlanView({
  plan,
  loading,
  error,
  onRefresh,
}: {
  plan: PlanNode | null
  loading: boolean
  error: string | null
  onRefresh: () => void
}) {
  if (loading) {
    return (
      <div className="grid h-full place-items-center p-6 text-center">
        <div>
          <Loader2 className="mx-auto size-5 animate-spin text-primary" />
          <p className="mt-2 text-sm text-muted-foreground">Building execution plan…</p>
        </div>
      </div>
    )
  }
  if (error) {
    return (
      <div className="p-5">
        <ErrorNotice message={error} />
        <Button variant="outline" size="sm" className="mt-3" onClick={onRefresh}><RefreshCw className="size-3.5" /> Retry</Button>
      </div>
    )
  }
  if (!plan) {
    return (
      <div className="grid h-full place-items-center p-6 text-center">
        <div className="max-w-sm">
          <div className="mx-auto mb-3 grid size-10 place-items-center rounded-lg bg-primary/10 text-primary"><GitMerge className="size-5" /></div>
          <div className="text-sm font-medium">No plan yet</div>
          <p className="mt-1 text-[12px] text-muted-foreground">Select this tab or click Explain to build an operator tree without reading data.</p>
          <Button size="sm" className="mt-4" onClick={onRefresh}><RefreshCw className="size-3.5" /> Explain query</Button>
        </div>
      </div>
    )
  }
  const stats = collectStats(plan)
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/45 bg-muted/20 px-4 py-2.5">
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <Badge variant="outline" className="font-mono text-[9px]">EXPLAIN</Badge>
          <span>{stats.operators} operators</span>
          <span className="size-0.5 rounded-full bg-current" />
          <span>{stats.sources} sources</span>
          {stats.scans.slice(0, 3).map((s) => <code key={s} className="rounded bg-background px-1.5 py-0.5 font-mono text-[9px]">{s}</code>)}
        </div>
        <Button variant="ghost" size="xs" onClick={onRefresh}><RefreshCw className="size-3" /> Refresh</Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-4 scrollbar-thin">
        <div className="mb-3 flex items-center gap-2 text-[10.5px] text-muted-foreground">
          <CircleAlert className="size-3 text-warning" /> Plan only — no source rows were read.
        </div>
        <PlanNodeCard node={plan} depth={0} />
      </div>
    </div>
  )
}
