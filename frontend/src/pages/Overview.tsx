import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router-dom"
import {
  Activity,
  ArrowRight,
  Bookmark,
  CheckCircle2,
  CircleAlert,
  Clock3,
  Database,
  Layers,
  Play,
  SquareTerminal,
  Users,
} from "lucide-react"
import { api } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import { useI18n } from "@/i18n"
import { formatDuration, formatNumber, initials, relativeTime, shortRelative, truncate } from "@/lib/utils"
import { Page, PageBody, PageHeader, StatCard, StatusBadge, EmptyState, InlineCode } from "@/components/shared"
import { SourceIcon } from "@/components/SourceIcon"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import type { Role } from "@/lib/types"

const sampleQuery = `FROM mongo.orders
|> WHERE status = 'paid'
|> AGGREGATE region, count(*) AS orders, sum(total) AS revenue GROUP BY region
|> ORDER BY revenue DESC`

export function OverviewPage() {
  const { t } = useI18n()
  const { tenant, role } = useTenant()
  const { data: overview, isLoading } = useQuery({ queryKey: ["overview", tenant.slug], queryFn: () => api.overview(tenant.slug) })
  const { data: sources } = useQuery({ queryKey: ["datasources", tenant.slug], queryFn: () => api.dataSources(tenant.slug) })
  const { data: members } = useQuery({ queryKey: ["members", tenant.slug], queryFn: () => api.members(tenant.slug) })
  const memberById = new Map(members?.members.map((m) => [m.user.id, m.user]) ?? [])
  const sourceList = (sources?.dataSources ?? []).map((d) => ({ ...d, tables: d.tables ?? [] }))
  const readySources = sourceList.filter((d) => d.status === "ready")

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow={tenant.name}
          title={role === "owner" ? t("Good to see you, owner.") : "Good to see you."}
          description={t("Your federated data workspace — connect sources, explore schemas and share queries with the team.")}
          actions={
            <>
              <Button variant="outline" asChild><Link to={`/w/${tenant.slug}/sources`}><Database className="size-4" /> {t("Add a source")}</Link></Button>
              <Button asChild><Link to={`/w/${tenant.slug}/query`}><Play className="size-4" /> {t("New query")}</Link></Button>
            </>
          }
        />

        <div className="mt-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {isLoading ? (
            Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-[116px] rounded-xl" />)
          ) : (
            <>
              <StatCard label={t("Data sources")} value={overview?.dataSources ?? 0} icon={Database} detail={t("{connected} connected · {tables} tables", { connected: overview?.readyDataSources ?? 0, tables: overview?.tables ?? 0 })} />
              <StatCard label={t("Team members")} value={overview?.members ?? 0} icon={Users} detail={t("You are {role}", { role })} />
              <StatCard label={t("Saved queries")} value={overview?.savedQueries ?? 0} icon={Bookmark} detail={t("Shared with the workspace")} />
              <StatCard label={t("Tables available")} value={overview?.tables ?? 0} icon={Layers} detail={t("Across all connected sources")} />
            </>
          )}
        </div>

        <div className="mt-8 grid items-start gap-5 xl:grid-cols-[1.45fr_1fr]">
          <section className="surface">
            <div className="flex items-center justify-between border-b border-border/55 px-5 pt-5 pb-3">
              <div>
                <h2 className="text-sm font-semibold">{t("Recent query activity")}</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">{t("The latest runs across this workspace")}</p>
              </div>
              <Button variant="ghost" size="sm" asChild><Link to={`/w/${tenant.slug}/activity`}>{t("View all")} <ArrowRight className="size-3.5" /></Link></Button>
            </div>
            {isLoading ? (
              <div className="space-y-2 p-4">{Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div>
            ) : !(overview?.recentRuns ?? []).length ? (
              <EmptyState icon={Activity} title={t("No queries yet")} description={t("Run your first federated query to see it here.")} className="border-0 bg-transparent py-12" action={<Button asChild size="sm"><Link to={`/w/${tenant.slug}/query`}>{t("Open query studio")}</Link></Button>} />
            ) : (
              <div className="divide-y divide-border/45">
                {(overview?.recentRuns ?? []).map((run) => {
                  const user = memberById.get(run.userId)
                  return (
                    <Link key={run.id} to={`/w/${tenant.slug}/query`} className="flex items-center gap-3 px-5 py-3 transition-colors hover:bg-muted/35">
                      <div className={cn("grid size-8 shrink-0 place-items-center rounded-lg", run.status === "success" ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive")}>
                        {run.status === "success" ? <CheckCircle2 className="size-4" /> : <CircleAlert className="size-4" />}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-mono text-[11.5px]">{truncate(run.sql.replace(/\s+/g, " "), 90)}</div>
                        <div className="mt-1 flex items-center gap-2 text-[11px] text-muted-foreground">
                          {user ? (
                            <span className="inline-flex items-center gap-1.5"><Avatar className="size-4"><AvatarFallback className="text-[8px]">{initials(user.name, user.email)}</AvatarFallback></Avatar>{user.name}</span>
                          ) : <span>Teammate</span>}
                          <span className="size-0.5 rounded-full bg-current" />
                          <span>{relativeTime(run.createdAt)}</span>
                          <span className="size-0.5 rounded-full bg-current" />
                          <span>{formatDuration(run.durationMs)}</span>
                        </div>
                      </div>
                      <Badge variant={run.status === "success" ? "success" : "destructive"} className="hidden sm:inline-flex">
                        {run.status === "success" ? `${formatNumber(run.rowCount)} rows` : "Failed"}
                      </Badge>
                    </Link>
                  )
                })}
              </div>
            )}
          </section>

          <section className="surface">
            <div className="flex items-center justify-between border-b border-border/55 px-5 pt-5 pb-3">
              <div>
                <h2 className="text-sm font-semibold">{t("Source health")}</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">{t("{ready} of {total} connected", { ready: readySources.length, total: sourceList.length })}</p>
              </div>
              <Button variant="ghost" size="icon-sm" asChild><Link to={`/w/${tenant.slug}/sources`}><ArrowRight className="size-4" /></Link></Button>
            </div>
            {!sources ? (
              <div className="space-y-2 p-4">{Array.from({ length: 2 }).map((_, i) => <Skeleton key={i} className="h-14" />)}</div>
            ) : !sourceList.length ? (
              <div className="p-5">
                <p className="text-[13px] text-muted-foreground">{t("No sources connected yet.")}</p>
                <Button size="sm" className="mt-3" asChild><Link to={`/w/${tenant.slug}/sources`}><Database className="size-4" /> {t("Connect a source")}</Link></Button>
              </div>
            ) : (
              <div className="divide-y divide-border/45">
                {sourceList.slice(0, 5).map((ds) => (
                  <div key={ds.id} className="flex items-center gap-3 px-5 py-3">
                    <SourceIcon type={ds.type} icon={ds.icon} size="sm" />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-[13px] font-medium">{ds.name}</span>
                        <Badge variant="outline" className="text-[10px]">{ds.typeName}</Badge>
                      </div>
                      <div className="mt-0.5 flex items-center gap-1.5 whitespace-nowrap text-[11px] text-muted-foreground">
                        <span>{t("{count} tables", { count: ds.tables.length })}</span>
                        <span className="size-0.5 rounded-full bg-current" />
                        <span className="whitespace-nowrap">{ds.lastSyncedAt ? t("synced {time}", { time: shortRelative(ds.lastSyncedAt) }) : t("not synced")}</span>
                      </div>
                    </div>
                    <StatusBadge status={ds.status} />
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>

        <section className="relative mt-6 overflow-hidden rounded-xl border bg-[oklch(0.18_0.025_268)] p-6 text-white md:p-8">
          <div className="absolute inset-0 dot-bg opacity-[0.08]" />
          <div className="absolute -top-24 right-0 size-72 rounded-full bg-indigo-500/25 blur-[80px]" />
          <div className="relative grid gap-8 lg:grid-cols-[1.1fr_1fr] lg:items-center">
            <div>
              <div className="mb-3 inline-flex items-center gap-1.5 rounded-full border border-white/12 bg-white/5 px-2.5 py-1 text-[10.5px] font-medium text-white/60">
                <SquareTerminal className="size-3" /> {t("Query pipelines")}
              </div>
              <h2 className="text-xl font-semibold tracking-tight">{t("Federate first, model later.")}</h2>
              <p className="mt-2 max-w-lg text-[13px] leading-relaxed text-white/55">
                {t("Join MongoDB collections with SQL tables in one query. Use classic SQL or composable pipeline stages — the same workspace handles both.")}
              </p>
              <div className="mt-5 flex flex-wrap gap-2">
                <Button size="sm" asChild className="bg-white text-[oklch(0.2_0.02_265)] hover:bg-white/90"><Link to={`/w/${tenant.slug}/query`}><Play className="size-3.5" /> {t("Open query studio")}</Link></Button>
                <Button size="sm" variant="ghost" className="text-white/70 hover:bg-white/10 hover:text-white" asChild><Link to={`/w/${tenant.slug}/saved`}>{t("Explore saved queries")} <ArrowRight className="size-3.5" /></Link></Button>
              </div>
            </div>
            <div className="rounded-xl border border-white/10 bg-black/20 p-4 font-mono text-[11px] leading-[1.9] text-white/70 shadow-xl backdrop-blur">
              <div className="mb-2 flex items-center justify-between border-b border-white/8 pb-2 text-[10px] text-white/35">
                <span>pipeline.sql</span><span className="inline-flex items-center gap-1"><Clock3 className="size-2.5" /> live</span>
              </div>
              {sampleQuery.split("\n").map((line, i) => (
                <div key={i} className={cn(line.trim().startsWith("|>") && "text-cyan-300/90")}>{line}</div>
              ))}
            </div>
          </div>
        </section>

        {readySources.length === 0 && (
          <div className="mt-6 flex flex-col gap-3 surface-quiet px-5 py-4 text-[13px] sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2 text-muted-foreground"><CircleAlert className="size-4 text-warning" /> {t("Connect a data source to unlock the query studio.")}</div>
            <Button size="sm" variant="outline" asChild><Link to={`/w/${tenant.slug}/sources`}>{t("Connect data")} <ArrowRight className="size-3.5" /></Link></Button>
          </div>
        )}

        <div className="mt-6 grid gap-3 sm:grid-cols-3">
          {[
            { title: t("Connect"), body: t("Add MongoDB, Postgres, Snowflake, a REST endpoint or CSV file."), to: `/w/${tenant.slug}/sources`, icon: Database },
            { title: t("Explore"), body: t("Browse schemas, then insert qualified table names into the editor."), to: `/w/${tenant.slug}/query`, icon: Layers },
            { title: t("Collaborate"), body: t("Invite teammates, save queries, and review activity together."), to: `/w/${tenant.slug}/team`, icon: Users },
          ].map((item) => (
            <Link key={item.title} to={item.to} className="group surface p-4 transition-all hover:-translate-y-0.5 hover:shadow-md">
              <div className="mb-3 grid size-8 place-items-center rounded-lg bg-primary/8 text-primary"><item.icon className="size-4" /></div>
              <div className="flex items-center justify-between text-[13px] font-semibold">{item.title}<ArrowRight className="size-3.5 text-muted-foreground transition-transform group-hover:translate-x-0.5" /></div>
              <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">{item.body}</p>
            </Link>
          ))}
        </div>

        <div className="mt-8 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span>{t("Workspace role:")}</span><Badge variant="outline" className="capitalize">{role satisfies Role}</Badge>
          <span className="mx-1">·</span>
          <span>{t("Use")}</span><InlineCode>`source.table`</InlineCode><span>{t("to reference any connected table.")}</span>
        </div>
      </PageBody>
    </Page>
  )
}
