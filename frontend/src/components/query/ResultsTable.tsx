import { useEffect, useMemo, useState } from "react"
import { ArrowDown, ArrowUp, ArrowUpDown, CheckCircle2, ChevronLeft, ChevronRight, CircleAlert, Clock3, Copy, Loader2, Rows3, Table2 } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import type { QueryResult } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { EmptyState, ErrorNotice } from "@/components/shared"
import { formatDuration, formatNumber } from "@/lib/utils"

const PAGE_SIZE = 50

export function ResultsTable({
  result,
  running,
  error,
  errorCode,
  errorHint,
}: {
  result: QueryResult | null
  running: boolean
  error: string | null
  errorCode?: string | null
  errorHint?: string | null
}) {
  const [sort, setSort] = useState<{ column: string; dir: "asc" | "desc" } | null>(null)
  const [page, setPage] = useState(0)

  useEffect(() => {
    setSort(null)
    setPage(0)
  }, [result])

  const sortedRows = useMemo(() => {
    if (!result) return []
    if (!sort) return result.rows
    return [...result.rows].sort((a, b) => compare(a[sort.column], b[sort.column]) * (sort.dir === "asc" ? 1 : -1))
  }, [result, sort])

  const pageCount = Math.max(1, Math.ceil(sortedRows.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  const rows = sortedRows.slice(current * PAGE_SIZE, current * PAGE_SIZE + PAGE_SIZE)

  if (running) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
        <div className="relative grid size-12 place-items-center rounded-2xl border bg-card">
          <Loader2 className="size-5 animate-spin text-primary" />
        </div>
        <div className="text-sm">Running query…</div>
        <div className="text-[11px] text-muted-foreground/70">Scanning sources and executing the pipeline</div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-5">
        <ErrorNotice message={error} hint={errorHint} />
        {errorCode && (
          <div className="mt-3 flex items-center gap-2 text-[11px] text-muted-foreground">
            <Badge variant="destructive" className="font-mono">{errorCode}</Badge>
            <span>Fix the query and run again.</span>
          </div>
        )}
      </div>
    )
  }

  if (!result) {
    return (
      <div className="grid h-full place-items-center p-6">
        <div className="max-w-md text-center">
          <div className="mx-auto mb-4 grid size-11 place-items-center rounded-xl border bg-card text-muted-foreground">
            <Table2 className="size-5" />
          </div>
          <div className="text-sm font-medium">Results will appear here</div>
          <p className="mt-1 text-[12.5px] leading-relaxed text-muted-foreground">
            Write SQL and press <kbd className="rounded border bg-muted px-1 font-mono text-[10px]">⌘ ↵</kbd> or click Run. Tables are referenced as <span className="font-mono text-[11px]">`source`.`table`</span>.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {result.truncated && (
        <div className="border-b bg-warning/8 px-4 py-2 text-[11.5px] text-warning-foreground dark:text-warning">
          Results were truncated by the server row limit. Add filters or a LIMIT to refine.
        </div>
      )}
      {!!result.warnings?.length && (
        <div className="border-b bg-warning/8 px-4 py-2 text-[11.5px] text-warning-foreground dark:text-warning">
          {result.warnings.map((w, i) => <div key={i}>{w}</div>)}
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto scrollbar-thin">
        {result.columns.length === 0 ? (
          <EmptyState icon={CheckCircle2} title="Query completed" description="No columns were returned." className="m-5 border-0" />
        ) : result.rows.length === 0 ? (
          <EmptyState icon={Rows3} title="No rows returned" description="The query ran successfully but matched no rows." className="m-5 border-0" />
        ) : (
          <table className="w-full border-separate border-spacing-0 text-left text-[12px]">
            <thead className="sticky top-0 z-10">
              <tr>
                <th className="sticky left-0 z-20 w-12 border-r border-b bg-muted/80 px-2 py-2 text-right font-mono text-[10px] font-normal text-muted-foreground backdrop-blur">#</th>
                {result.columns.map((col) => (
                  <th
                    key={col.name}
                    className="min-w-[160px] cursor-pointer border-b bg-muted/80 px-3 py-2 font-normal whitespace-nowrap backdrop-blur select-none hover:bg-muted"
                    onClick={() =>
                      setSort((s) =>
                        s?.column === col.name ? (s.dir === "asc" ? { column: col.name, dir: "desc" } : null) : { column: col.name, dir: "asc" },
                      )
                    }
                  >
                    <div className="flex items-center gap-1.5">
                      <span className="text-[11px] font-semibold">{col.name}</span>
                      <span className="text-[9.5px] font-normal text-muted-foreground/70">{col.type}</span>
                      {sort?.column === col.name ? (
                        sort.dir === "asc" ? <ArrowUp className="ml-auto size-3 text-primary" /> : <ArrowDown className="ml-auto size-3 text-primary" />
                      ) : (
                        <ArrowUpDown className="ml-auto size-3 text-muted-foreground/35" />
                      )}
                    </div>
                  </th>
                ))}
                <th className="w-9 border-b bg-muted/80" />
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i} className="group transition-colors hover:bg-primary/[0.035]">
                  <td className="sticky left-0 z-[5] border-r border-b border-border/60 bg-background px-2 py-1.5 text-right font-mono text-[10px] text-muted-foreground/60 group-hover:bg-[color-mix(in_oklch,var(--background),var(--primary)_3%)]">
                    {current * PAGE_SIZE + i + 1}
                  </td>
                  {result.columns.map((col) => (
                    <td key={col.name} className="max-w-[420px] truncate border-b border-border/60 px-3 py-1.5 align-top">
                      <CellValue value={row[col.name]} type={col.type} />
                    </td>
                  ))}
                  <td className="border-b border-border/60 px-1 py-1.5">
                    <button
                      className="grid size-6 place-items-center rounded text-muted-foreground/40 opacity-0 transition-opacity group-hover:opacity-100 hover:bg-accent hover:text-foreground"
                      onClick={() => void navigator.clipboard.writeText(JSON.stringify(row, null, 2)).then(() => toast.success("Row copied as JSON"))}
                      title="Copy row"
                    >
                      <Copy className="size-3" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-t bg-card/60 px-3 py-2 text-[11px] text-muted-foreground">
        <div className="flex flex-wrap items-center gap-3">
          <span className="inline-flex items-center gap-1.5"><Rows3 className="size-3" /> {formatNumber(result.rowCount)} rows</span>
          <span className="inline-flex items-center gap-1.5"><Clock3 className="size-3" /> {formatDuration(result.stats.durationMs)}</span>
          {(result.stats.sources ?? []).map((s) => (
            <span key={`${s.source}.${s.table}`} className="hidden items-center gap-1 rounded border bg-background px-1.5 py-0.5 font-mono text-[9.5px] md:inline-flex">
              {s.source}.{s.table} <span className="text-muted-foreground/60">{s.rows}</span>
            </span>
          ))}
          <span className="hidden items-center gap-1 text-[10px] text-muted-foreground/70 lg:inline-flex">
            {(result.stats.stages ?? []).join(" → ")}
          </span>
        </div>
        {sortedRows.length > PAGE_SIZE && (
          <div className="flex items-center gap-1">
            <span className="mr-2">{current * PAGE_SIZE + 1}–{Math.min((current + 1) * PAGE_SIZE, sortedRows.length)} of {formatNumber(sortedRows.length)}</span>
            <Button variant="outline" size="icon-xs" disabled={current === 0} onClick={() => setPage((p) => Math.max(0, p - 1))}><ChevronLeft className="size-3.5" /></Button>
            <Button variant="outline" size="icon-xs" disabled={current >= pageCount - 1} onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}><ChevronRight className="size-3.5" /></Button>
          </div>
        )}
      </div>
    </div>
  )
}

function CellValue({ value, type }: { value: unknown; type: string }) {
  if (value === null || value === undefined) return <span className="font-mono text-[10.5px] text-muted-foreground/45 italic">NULL</span>
  if (typeof value === "boolean") {
    return <Badge variant={value ? "success" : "muted"} className="font-mono text-[9.5px]">{String(value)}</Badge>
  }
  if (typeof value === "object") {
    const text = JSON.stringify(value)
    return <span className="font-mono text-[10.5px] text-muted-foreground" title={text}>{text}</span>
  }
  if (type === "integer" || type === "number") {
    return <span className="font-mono text-[11.5px] tabular-nums">{String(value)}</span>
  }
  if (type === "timestamp") {
    return <span className="font-mono text-[11px]">{String(value)}</span>
  }
  return <span className="font-mono text-[11.5px]">{String(value)}</span>
}

function compare(a: unknown, b: unknown) {
  if (a === b) return 0
  if (a === null || a === undefined) return 1
  if (b === null || b === undefined) return -1
  if (typeof a === "number" && typeof b === "number") return a - b
  return String(a).localeCompare(String(b), undefined, { numeric: true })
}
