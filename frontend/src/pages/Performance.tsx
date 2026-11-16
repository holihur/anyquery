import { useQuery } from "@tanstack/react-query"
import { Activity, CheckCircle2, CircleAlert, Clock3, Database, Gauge, HardDrive, Zap } from "lucide-react"
import { useTenant } from "@/providers/tenant"
import { api } from "@/lib/api"
import { formatNumber } from "@/lib/utils"
import { Page, PageBody, PageHeader, StatCard, LoadingBlock, EmptyState } from "@/components/shared"
import { Badge } from "@/components/ui/badge"

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB"]
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

export function PerformancePage() {
  const { tenant, role } = useTenant()
  const isAdmin = role === "owner" || role === "admin"
  const { data, isLoading, error } = useQuery({
    queryKey: ["metrics", tenant.slug],
    queryFn: () => api.metrics(tenant.slug),
    enabled: isAdmin,
    refetchInterval: 5_000,
  })

  if (!isAdmin) {
    return (
      <Page><PageBody>
        <EmptyState icon={Gauge} title="Admin access required" description="Performance metrics are available to workspace owners and admins." />
      </PageBody></Page>
    )
  }

  const m = data?.metrics
  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow={<span className="inline-flex items-center gap-1.5"><Gauge className="size-3" /> Observability</span>}
          title="Performance"
          description="Live query telemetry for this workspace, including cache efficiency and latency percentiles. Refreshes every 5 seconds."
          actions={<Badge variant="outline" className="gap-1"><Activity className="size-3" /> in-process</Badge>}
        />

        {isLoading ? <LoadingBlock className="mt-8" /> : error ? (
          <EmptyState icon={CircleAlert} title="Could not load metrics" description={error instanceof Error ? error.message : "Try again shortly."} className="mt-5" />
        ) : (
          <>
            <div className="mt-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <StatCard label="Queries" value={formatNumber(m?.total ?? 0)} icon={Activity} detail={`${formatNumber(m?.success ?? 0)} succeeded · ${formatNumber(m?.errors ?? 0)} failed`} />
              <StatCard label="Cache hit rate" value={`${(m?.cacheHitPct ?? 0).toFixed(1)}%`} icon={Zap} detail={`${formatNumber(m?.cacheHits ?? 0)} hits · ${formatNumber(m?.cacheMisses ?? 0)} misses`} />
              <StatCard label="P50 latency" value={`${(m?.p50Ms ?? 0).toFixed(1)} ms`} icon={Clock3} detail={`P95 ${(m?.p95Ms ?? 0).toFixed(1)} ms`} />
              <StatCard label="Scanned rows" value={formatNumber(m?.scannedRows ?? 0)} icon={Database} detail="Across source connectors" />
            </div>

            <div className="mt-6 grid gap-4 lg:grid-cols-[1.1fr_0.9fr]">
              <section className="surface p-5">
                <div className="flex items-center justify-between">
                  <div>
                    <h2 className="text-sm font-semibold">Latency distribution</h2>
                    <p className="mt-0.5 text-xs text-muted-foreground">Recent completed queries, sampled in process.</p>
                  </div>
                  <Badge variant="success" className="gap-1"><CheckCircle2 className="size-3" /> healthy</Badge>
                </div>
                <div className="mt-6 space-y-5">
                  {[
                    { label: "P50", value: m?.p50Ms ?? 0, max: Math.max(m?.p95Ms ?? 0, 1) },
                    { label: "P95", value: m?.p95Ms ?? 0, max: Math.max(m?.p95Ms ?? 0, 1) },
                  ].map((row) => (
                    <div key={row.label}>
                      <div className="flex items-center justify-between text-[11px]"><span className="text-muted-foreground">{row.label}</span><span className="font-mono">{row.value.toFixed(1)} ms</span></div>
                      <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-muted">
                        <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${Math.min(100, (row.value / row.max) * 100)}%` }} />
                      </div>
                    </div>
                  ))}
                </div>
                <div className="mt-6 grid grid-cols-2 gap-3 border-t border-border/50 pt-4 text-[11px]">
                  <div className="rounded-lg bg-muted/35 p-3"><div className="text-muted-foreground">Succeeded</div><div className="mt-1 text-base font-semibold tabular-nums">{formatNumber(m?.success ?? 0)}</div></div>
                  <div className="rounded-lg bg-muted/35 p-3"><div className="text-muted-foreground">Failed</div><div className="mt-1 text-base font-semibold tabular-nums">{formatNumber(m?.errors ?? 0)}</div></div>
                </div>
              </section>

              <section className="surface p-5">
                <div className="flex items-center justify-between">
                  <div>
                    <h2 className="text-sm font-semibold">Query cache</h2>
                    <p className="mt-0.5 text-xs text-muted-foreground">Tenant result cache state.</p>
                  </div>
                  <Badge variant={data?.cache.enabled ? "success" : "muted"}>{data?.cache.enabled ? data?.cache.mode ?? "enabled" : "disabled"}</Badge>
                </div>
                <div className="mt-5 space-y-3 text-[12px]">
                  <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2.5"><span className="inline-flex items-center gap-1.5 text-muted-foreground"><HardDrive className="size-3.5" /> Retained bytes</span><span className="font-mono">{formatBytes(data?.cache.bytes ?? 0)}</span></div>
                  <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2.5"><span className="text-muted-foreground">Entries</span><span className="font-mono">{formatNumber(data?.cache.entries ?? 0)}</span></div>
                  <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2.5"><span className="text-muted-foreground">Idle connectors</span><span className="font-mono">{formatNumber(data?.cache.idleConnectors ?? 0)}</span></div>
                  <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2.5"><span className="text-muted-foreground">Uptime</span><span className="font-mono">{Math.floor((m?.uptimeSec ?? 0) / 60)}m</span></div>
                </div>
                <div className="mt-5 rounded-lg bg-muted/25 px-3 py-2.5 text-[11px] leading-relaxed text-muted-foreground">
                  Cache hits bypass source scans. Writes to any data source invalidate this workspace's cache immediately.
                </div>
              </section>
            </div>

            <div className="mt-5 grid gap-3 sm:grid-cols-3">
              <div className="surface-quiet p-4"><div className="flex items-center gap-2 text-[11px] text-muted-foreground"><Activity className="size-3.5" /> Query volume</div><div className="mt-2 text-lg font-semibold tabular-nums">{formatNumber(m?.total ?? 0)}</div></div>
              <div className="surface-quiet p-4"><div className="flex items-center gap-2 text-[11px] text-muted-foreground"><Zap className="size-3.5" /> Saved source work</div><div className="mt-2 text-lg font-semibold tabular-nums">{formatNumber(m?.cacheHits ?? 0)}</div></div>
              <div className="surface-quiet p-4"><div className="flex items-center gap-2 text-[11px] text-muted-foreground"><Database className="size-3.5" /> Scanned</div><div className="mt-2 text-lg font-semibold tabular-nums">{formatNumber(m?.scannedRows ?? 0)}</div></div>
            </div>
          </>
        )}
      </PageBody>
    </Page>
  )
}
