import { memo, useEffect, useId, useRef, useState } from "react"
import type { DashboardVisualization, QueryResult, ResultColumn } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const COLORS = ["#6366f1", "#06b6d4", "#f59e0b", "#ec4899", "#22c55e", "#ef4444", "#8b5cf6", "#14b8a6"]
const CHART_TYPES: { value: DashboardVisualization["type"]; label: string }[] = [
  { value: "table", label: "Table" },
  { value: "bar", label: "Bar" },
  { value: "line", label: "Line" },
  { value: "area", label: "Area" },
  { value: "pie", label: "Pie" },
  { value: "scatter", label: "Scatter" },
  { value: "stat", label: "Stat" },
]

export function ChartTypeLabel(type: string) {
  return CHART_TYPES.find((item) => item.value === type)?.label ?? type
}

function toNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value
  if (typeof value === "string" && value.trim() !== "") {
    const n = Number(value)
    if (Number.isFinite(n)) return n
  }
  return undefined
}

function formatNumber(value: number): string {
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}m`
  if (Math.abs(value) >= 1_000) return `${(value / 1_000).toFixed(1)}k`
  if (Number.isInteger(value)) return value.toLocaleString()
  return value.toFixed(2).replace(/\.?0+$/, "")
}

function displayValue(value: unknown): string {
  if (value === null || value === undefined) return ""
  if (typeof value === "object") return JSON.stringify(value)
  return String(value)
}

function numericColumns(result: QueryResult): string[] {
  return result.columns.filter((c) => c.type === "integer" || c.type === "number").map((c) => c.name)
}

function seriesFor(result: QueryResult, config: DashboardVisualization): string[] {
  const numeric = numericColumns(result)
  const selected = (config.y ?? []).filter((name) => result.columns.some((c) => c.name === name))
  if (selected.length) return selected
  return numeric.filter((name) => name !== config.x).slice(0, 3)
}

function xColumn(result: QueryResult, config: DashboardVisualization): string {
  if (config.x && result.columns.some((c) => c.name === config.x)) return config.x
  return result.columns.find((c) => !numericColumns(result).includes(c.name))?.name ?? result.columns[0]?.name ?? ""
}

function niceMax(value: number): number {
  if (value <= 0) return 1
  const power = Math.pow(10, Math.floor(Math.log10(value)))
  return Math.ceil(value / power) * power
}

function ChartRendererImpl({
  result,
  config,
  className,
}: {
  result: QueryResult
  config: DashboardVisualization
  className?: string
}) {
  if (!result.rows.length) {
    return <div className={cn("grid h-full place-items-center text-[12px] text-muted-foreground", className)}>No rows returned</div>
  }
  if (config.type === "table") return <MiniTable result={result} className={className} />
  if (config.type === "stat") return <StatView result={result} config={config} className={className} />
  if (config.type === "pie") return <PieView result={result} config={config} className={className} />
  if (config.type === "scatter") return <ScatterView result={result} config={config} className={className} />
  return <CartesianView result={result} config={config} className={className} />
}

function MiniTable({ result, className }: { result: QueryResult; className?: string }) {
  return (
    <div className={cn("h-full overflow-auto scrollbar-thin", className)}>
      <table className="w-full text-left text-[11px]">
        <thead className="sticky top-0 bg-muted/80 backdrop-blur">
          <tr>
            {result.columns.map((col) => (
              <th key={col.name} className="whitespace-nowrap border-b border-border/50 px-2 py-1.5 font-medium">{col.name}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.slice(0, 100).map((row, i) => (
            <tr key={i} className="hover:bg-muted/30">
              {result.columns.map((col) => (
                <td key={col.name} className="max-w-[240px] truncate border-b border-border/35 px-2 py-1 font-mono text-[10.5px]">
                  {displayValue(row[col.name])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function StatView({ result, config, className }: { result: QueryResult; config: DashboardVisualization; className?: string }) {
  const series = seriesFor(result, config)
  const name = series[0] ?? result.columns[0]?.name
  const value = name ? result.rows[0]?.[name] : undefined
  const n = toNumber(value)
  return (
    <div className={cn("flex h-full flex-col justify-center px-1", className)}>
      <div className="truncate text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{name || "Value"}</div>
      <div className="mt-1 truncate text-3xl font-semibold tracking-tight tabular-nums">
        {n === undefined ? displayValue(value) || "—" : formatNumber(n)}
      </div>
      <div className="mt-1 text-[10.5px] text-muted-foreground">{result.rows.length} {result.rows.length === 1 ? "row" : "rows"}</div>
    </div>
  )
}

type Point = { x: number; y: number; label: string; series: string; value: number }

function CartesianView({ result, config, className }: { result: QueryResult; config: DashboardVisualization; className?: string }) {
  const xName = xColumn(result, config)
  const series = seriesFor(result, config)
  const rows = result.rows.slice(0, 250)
  const points: Point[] = []
  for (const [rowIndex, row] of rows.entries()) {
    for (const name of series) {
      const value = toNumber(row[name])
      if (value === undefined) continue
      points.push({ x: rowIndex, y: value, label: displayValue(row[xName]) || String(rowIndex + 1), series: name, value })
    }
  }
  if (!points.length) return <div className={cn("grid h-full place-items-center text-[12px] text-muted-foreground", className)}>Pick numeric Y columns</div>
  const boxRef = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState({ w: 720, h: 280 })
  useEffect(() => {
    const element = boxRef.current
    if (!element) return
    const observer = new ResizeObserver(([entry]) => {
      const w = Math.max(280, entry.contentRect.width)
      const h = Math.max(140, entry.contentRect.height)
      setBox({ w, h })
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  const width = box.w
  const height = box.h
  const pad = { left: 48, right: 16, top: 16, bottom: 34 }
  const plotW = width - pad.left - pad.right
  const plotH = height - pad.top - pad.bottom
  const min = Math.min(0, ...points.map((p) => p.y))
  const max = niceMax(Math.max(...points.map((p) => p.y)))
  const span = max - min || 1
  const xAt = (index: number) => pad.left + (rows.length <= 1 ? plotW / 2 : (index / (rows.length - 1)) * plotW)
  const yAt = (value: number) => pad.top + plotH - ((value - min) / span) * plotH
  const ticks = Array.from({ length: 5 }, (_, i) => min + (span / 4) * i)
  const labelEvery = Math.max(1, Math.ceil(rows.length / 7))

  return (
    <div ref={boxRef} className={cn("flex h-full min-h-0 flex-col", className)}>
      <svg viewBox={`0 0 ${width} ${height}`} className="h-full w-full" role="img" aria-label="Chart">
        {ticks.map((tick, i) => (
          <g key={i}>
            <line x1={pad.left} x2={width - pad.right} y1={yAt(tick)} y2={yAt(tick)} stroke="currentColor" strokeOpacity="0.1" strokeDasharray="3 4" />
            <text x={pad.left - 7} y={yAt(tick) + 3} textAnchor="end" className="fill-muted-foreground text-[9px]">{formatNumber(tick)}</text>
          </g>
        ))}
        {config.type === "bar" &&
          rows.map((row, i) => {
            const groupWidth = plotW / Math.max(rows.length, 1)
            const barWidth = Math.max(1, (groupWidth * 0.72) / Math.max(series.length, 1))
            return series.map((name, si) => {
              const value = toNumber(row[name])
              if (value === undefined) return null
              const x = pad.left + i * groupWidth + groupWidth * 0.14 + si * barWidth
              const y = yAt(Math.max(0, value))
              const h = Math.max(1, Math.abs(yAt(value) - yAt(0)))
              return <rect key={`${i}-${name}`} x={x} y={value >= 0 ? y : yAt(0)} width={Math.max(1, barWidth - 2)} height={h} rx="1.5" fill={COLORS[si % COLORS.length]} opacity="0.9"><title>{`${name} · ${displayValue(row[xName])}: ${formatNumber(value)}`}</title></rect>
            })
          })}
        {config.type !== "bar" &&
          series.map((name, si) => {
            const seriesPoints = points.filter((p) => p.series === name)
            if (!seriesPoints.length) return null
            const d = seriesPoints.map((p, i) => `${i === 0 ? "M" : "L"}${xAt(p.x).toFixed(1)},${yAt(p.y).toFixed(1)}`).join(" ")
            const area = `${d} L${xAt(seriesPoints[seriesPoints.length - 1].x).toFixed(1)},${yAt(min).toFixed(1)} L${xAt(seriesPoints[0].x).toFixed(1)},${yAt(min).toFixed(1)} Z`
            return (
              <g key={name}>
                {config.type === "area" && <path d={area} fill={COLORS[si % COLORS.length]} opacity="0.12" />}
                <path d={d} fill="none" stroke={COLORS[si % COLORS.length]} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
                {seriesPoints.map((p) => <circle key={`${p.series}-${p.x}`} cx={xAt(p.x)} cy={yAt(p.y)} r="2.5" fill={COLORS[si % COLORS.length]}><title>{`${name} · ${p.label}: ${formatNumber(p.value)}`}</title></circle>)}
              </g>
            )
          })}
        {rows.map((row, i) =>
          i % labelEvery === 0 ? <text key={i} x={xAt(i)} y={height - 12} textAnchor="middle" className="fill-muted-foreground text-[9px]">{displayValue(row[xName]).slice(0, 12)}</text> : null,
        )}
      </svg>
      <Legend series={series} />
    </div>
  )
}

function PieView({ result, config, className }: { result: QueryResult; config: DashboardVisualization; className?: string }) {
  const xName = xColumn(result, config)
  const yName = seriesFor(result, config)[0]
  if (!yName) return <div className={cn("grid h-full place-items-center text-[12px] text-muted-foreground", className)}>Pick a numeric Y column</div>
  const totals = new Map<string, number>()
  for (const row of result.rows.slice(0, 1000)) {
    const value = toNumber(row[yName])
    if (value === undefined) continue
    const key = displayValue(row[xName]) || "(empty)"
    totals.set(key, (totals.get(key) ?? 0) + value)
  }
  const entries = [...totals.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8)
  const total = entries.reduce((sum, [, value]) => sum + Math.max(0, value), 0)
  if (!entries.length || total <= 0) return <div className={cn("grid h-full place-items-center text-[12px] text-muted-foreground", className)}>No positive values</div>
  let angle = -Math.PI / 2
  const cx = 110
  const cy = 110
  const r = 82
  const inner = 48
  return (
    <div className={cn("flex h-full items-center gap-4 overflow-hidden", className)}>
      <svg viewBox="0 0 220 220" className="size-[min(48%,220px)] shrink-0">
        {entries.map(([label, value], i) => {
          const next = angle + (Math.max(0, value) / total) * Math.PI * 2
          const path = arcPath(cx, cy, r, inner, angle, next)
          const title = `${label}: ${formatNumber(value)} (${((value / total) * 100).toFixed(1)}%)`
          angle = next
          return <path key={label} d={path} fill={COLORS[i % COLORS.length]} stroke="var(--background)" strokeWidth="1.5"><title>{title}</title></path>
        })}
      </svg>
      <div className="min-w-0 flex-1 space-y-1.5 overflow-auto">
        {entries.map(([label, value], i) => (
          <div key={label} className="flex items-center gap-1.5 text-[11px]">
            <span className="size-2 shrink-0 rounded-full" style={{ background: COLORS[i % COLORS.length] }} />
            <span className="truncate text-muted-foreground">{label}</span>
            <span className="ml-auto font-mono tabular-nums">{formatNumber(value)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function arcPath(cx: number, cy: number, outer: number, inner: number, start: number, end: number): string {
  const large = end - start > Math.PI ? 1 : 0
  const p = (radius: number, angle: number) => `${(cx + Math.cos(angle) * radius).toFixed(2)},${(cy + Math.sin(angle) * radius).toFixed(2)}`
  return `M${p(outer, start)} A${outer},${outer} 0 ${large} 1 ${p(outer, end)} L${p(inner, end)} A${inner},${inner} 0 ${large} 0 ${p(inner, start)} Z`
}

function ScatterView({ result, config, className }: { result: QueryResult; config: DashboardVisualization; className?: string }) {
  const xName = config.x || numericColumns(result)[0]
  const yName = seriesFor(result, config)[0] ?? numericColumns(result)[1]
  const points = result.rows
    .map((row) => ({ x: toNumber(row[xName]), y: toNumber(row[yName]) }))
    .filter((p): p is { x: number; y: number } => p.x !== undefined && p.y !== undefined)
    .slice(0, 1000)
  if (!points.length) return <div className={cn("grid h-full place-items-center text-[12px] text-muted-foreground", className)}>Pick numeric X and Y columns</div>
  const width = 720
  const height = 280
  const pad = { left: 48, right: 18, top: 16, bottom: 34 }
  const xMax = niceMax(Math.max(...points.map((p) => p.x)))
  const yMax = niceMax(Math.max(...points.map((p) => p.y)))
  const xAt = (value: number) => pad.left + (value / xMax) * (width - pad.left - pad.right)
  const yAt = (value: number) => height - pad.bottom - (value / yMax) * (height - pad.top - pad.bottom)
  return (
    <div className={cn("h-full", className)}>
      <svg viewBox={`0 0 ${width} ${height}`} className="size-full" role="img" aria-label="Scatter chart">
        {[0, 0.25, 0.5, 0.75, 1].map((tick) => <line key={tick} x1={pad.left} x2={width - pad.right} y1={yAt(yMax * tick)} y2={yAt(yMax * tick)} stroke="currentColor" strokeOpacity="0.1" strokeDasharray="3 4" />)}
        {points.map((p, i) => <circle key={i} cx={xAt(p.x)} cy={yAt(p.y)} r="3.5" fill={COLORS[0]} opacity="0.65"><title>{`${xName}: ${formatNumber(p.x)} · ${yName}: ${formatNumber(p.y)}`}</title></circle>)}
        <text x={width - pad.right} y={height - 12} textAnchor="end" className="fill-muted-foreground text-[9px]">{xName}</text>
        <text x={pad.left} y={12} className="fill-muted-foreground text-[9px]">{yName}</text>
      </svg>
    </div>
  )
}

function Legend({ series }: { series: string[] }) {
  if (series.length <= 1) return null
  return (
    <div className="flex flex-wrap items-center justify-center gap-3 pb-1 text-[10px] text-muted-foreground">
      {series.map((name, i) => <span key={name} className="inline-flex items-center gap-1"><span className="size-2 rounded-sm" style={{ background: COLORS[i % COLORS.length] }} />{name}</span>)}
    </div>
  )
}

export const ChartRenderer = memo(ChartRendererImpl)

export function VisualizationBuilder({
  columns,
  config,
  onChange,
}: {
  columns: ResultColumn[]
  config: DashboardVisualization
  onChange: (config: DashboardVisualization) => void
}) {
  const id = useId()
  const numeric = columns.filter((c) => c.type === "integer" || c.type === "number")
  const selected = config.y ?? []
  const toggleY = (name: string) => {
    const next = selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name].slice(-6)
    onChange({ ...config, y: next })
  }
  const needsY = config.type !== "table"
  return (
    <div className="space-y-3 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={`${id}-type`} className="text-[11px] font-medium text-muted-foreground">Chart</label>
        <Select value={config.type} onValueChange={(value) => onChange({ ...config, type: value as DashboardVisualization["type"] })}>
          <SelectTrigger id={`${id}-type`} size="sm" className="h-7 text-[11px]"><SelectValue /></SelectTrigger>
          <SelectContent>{CHART_TYPES.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent>
        </Select>
        {config.type !== "table" && config.type !== "stat" && (
          <>
            <label htmlFor={`${id}-x`} className="ml-2 text-[11px] font-medium text-muted-foreground">X</label>
            <Select value={config.x || "__none"} onValueChange={(value) => onChange({ ...config, x: value === "__none" ? "" : value })}>
              <SelectTrigger id={`${id}-x`} size="sm" className="h-7 max-w-[180px] text-[11px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__none">Row index</SelectItem>
                {columns.map((column) => <SelectItem key={column.name} value={column.name}>{column.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </>
        )}
      </div>
      {needsY && (
        <div>
          <div className="mb-1.5 text-[11px] font-medium text-muted-foreground">Y columns</div>
          <div className="flex flex-wrap gap-1.5">
            {(numeric.length ? numeric : columns).map((column) => (
              <button
                key={column.name}
                type="button"
                onClick={() => toggleY(column.name)}
                className={cn(
                  "rounded-md border px-2 py-1 font-mono text-[10.5px] transition-colors",
                  selected.includes(column.name) ? "border-primary/40 bg-primary/10 text-primary" : "bg-background text-muted-foreground hover:bg-muted",
                )}
              >
                {column.name}
              </button>
            ))}
          </div>
          {!numeric.length && <p className="mt-1.5 text-[10px] text-muted-foreground">No numeric columns detected; values will be cast when possible.</p>}
        </div>
      )}
    </div>
  )
}
