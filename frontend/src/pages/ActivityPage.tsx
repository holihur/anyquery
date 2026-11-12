import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Activity, CheckCircle2, CircleAlert, Clock3, History, RefreshCw, ScrollText, ShieldCheck } from "lucide-react"
import { api } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import { formatDateTime, formatDuration, formatNumber, initials, relativeTime, truncate } from "@/lib/utils"
import { Page, PageBody, PageHeader, EmptyState, RoleBadge } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Skeleton } from "@/components/ui/skeleton"

export function ActivityPage() {
  const { tenant, role } = useTenant()
  const queryClient = useQueryClient()
  const isAdmin = role === "owner" || role === "admin"
  const [tab, setTab] = useState("history")

  const { data: runs, isLoading: runsLoading } = useQuery({ queryKey: ["runs", tenant.slug, "activity"], queryFn: () => api.runs(tenant.slug, 100) })
  const { data: members } = useQuery({ queryKey: ["members", tenant.slug], queryFn: () => api.members(tenant.slug) })
  const { data: audit, isLoading: auditLoading } = useQuery({ queryKey: ["audit", tenant.slug], queryFn: () => api.audit(tenant.slug, 150), enabled: isAdmin })
  const memberById = new Map(members?.members.map((m) => [m.user.id, m.user]) ?? [])

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["runs", tenant.slug] })
    void queryClient.invalidateQueries({ queryKey: ["audit", tenant.slug] })
  }

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow="Audit trail"
          title="Activity"
          description="Every query run and every change to the workspace, in one place."
          actions={<Button variant="outline" size="sm" onClick={refresh}><RefreshCw className="size-3.5" /> Refresh</Button>}
        />

        <Tabs value={tab} onValueChange={setTab} className="mt-5">
          <TabsList>
            <TabsTrigger value="history"><History className="size-3.5" /> Query history</TabsTrigger>
            {isAdmin && <TabsTrigger value="audit"><ScrollText className="size-3.5" /> Audit log</TabsTrigger>}
          </TabsList>

          <TabsContent value="history">
            <div className="overflow-hidden surface">
              <div className="flex items-center justify-between px-5 pt-5 pb-2">
                <div>
                  <h2 className="text-sm font-semibold">Query runs</h2>
                  <p className="mt-0.5 text-xs text-muted-foreground">The latest {runs?.runs?.length ?? 0} runs in this workspace</p>
                </div>
                <Badge variant="outline"><Activity className="size-3" /> live log</Badge>
              </div>
              {runsLoading ? (
                <div className="space-y-2 p-4">{Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-14" />)}</div>
              ) : !(runs?.runs ?? []).length ? (
                <EmptyState icon={History} title="No query runs yet" description="Run a query in the studio to start building history." className="border-0" />
              ) : (
                <div className="space-y-0.5 p-1.5">
                  {(runs?.runs ?? []).map((run) => {
                    const user = memberById.get(run.userId)
                    return (
                      <div key={run.id} className="flex items-start gap-3 px-5 py-3.5">
                        <div className={run.status === "success" ? "mt-0.5 grid size-7 shrink-0 place-items-center rounded-md bg-success/10 text-success" : "mt-0.5 grid size-7 shrink-0 place-items-center rounded-md bg-destructive/10 text-destructive"}>
                          {run.status === "success" ? <CheckCircle2 className="size-3.5" /> : <CircleAlert className="size-3.5" />}
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="line-clamp-1 font-mono text-[11.5px]">{truncate(run.sql.replace(/\s+/g, " "), 180)}</div>
                          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[10.5px] text-muted-foreground">
                            {user ? (
                              <span className="inline-flex items-center gap-1.5"><Avatar className="size-4"><AvatarFallback className="text-[8px]">{initials(user.name, user.email)}</AvatarFallback></Avatar>{user.name}</span>
                            ) : <span>Teammate</span>}
                            <span className="size-0.5 rounded-full bg-current" />
                            <span>{formatDateTime(run.createdAt)}</span>
                            <span className="size-0.5 rounded-full bg-current" />
                            <span>{formatDuration(run.durationMs)}</span>
                            <span className="size-0.5 rounded-full bg-current" />
                            <span>{run.status === "success" ? `${formatNumber(run.rowCount)} rows` : run.error || "failed"}</span>
                            {(run.sources ?? []).length > 0 && (
                              <>
                                <span className="size-0.5 rounded-full bg-current" />
                                <span className="font-mono">{(run.sources ?? []).join(" + ")}</span>
                              </>
                            )}
                          </div>
                        </div>
                        <Badge variant={run.status === "success" ? "success" : "destructive"} className="hidden sm:inline-flex">{run.status}</Badge>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </TabsContent>

          {isAdmin && (
            <TabsContent value="audit">
              <div className="overflow-hidden surface">
                <div className="flex items-center justify-between px-5 pt-5 pb-2">
                  <div>
                    <h2 className="text-sm font-semibold">Workspace audit log</h2>
                    <p className="mt-0.5 text-xs text-muted-foreground">Membership, connection and query changes</p>
                  </div>
                  <Badge variant="outline"><ShieldCheck className="size-3" /> admins only</Badge>
                </div>
                {auditLoading ? (
                  <div className="space-y-2 p-4">{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div>
                ) : !(audit?.logs ?? []).length ? (
                  <EmptyState icon={ScrollText} title="No audit events" description="Workspace activity will appear here." className="border-0" />
                ) : (
                  <div className="space-y-0.5 p-1.5">
                    {(audit?.logs ?? []).map((log) => (
                      <div key={log.id} className="flex items-start gap-3 px-5 py-3">
                        <div className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
                          <Clock3 className="size-3.5" />
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="text-[12px] font-medium">{formatAction(log.action)}</span>
                            {log.targetType && <Badge variant="muted" className="text-[9px]">{log.targetType}</Badge>}
                          </div>
                          <div className="mt-0.5 text-[10.5px] text-muted-foreground">
                            {log.actorName} · {formatDateTime(log.createdAt)} {log.ip ? `· ${log.ip}` : ""}
                          </div>
                        </div>
                        {log.metadata && Object.keys(log.metadata).length > 0 && (
                          <span className="hidden max-w-[220px] truncate font-mono text-[9.5px] text-muted-foreground/70 lg:block">{JSON.stringify(log.metadata)}</span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </TabsContent>
          )}
        </Tabs>
      </PageBody>
    </Page>
  )
}

function formatAction(action: string) {
  const map: Record<string, string> = {
    "datasource.create": "Data source connected",
    "datasource.update": "Data source updated",
    "datasource.delete": "Data source removed",
    "datasource.refresh": "Schema refreshed",
    "query.save": "Query saved",
    "query.update": "Query updated",
    "query.delete": "Query deleted",
    "member.role": "Member role changed",
    "member.remove": "Member removed",
    "member.join": "Member joined",
    "invite.create": "Invitation created",
    "invite.revoke": "Invitation revoked",
    "workspace.create": "Workspace created",
    "workspace.update": "Workspace updated",
    "apikey.create": "API key created",
    "apikey.revoke": "API key revoked",
    "auth.login": "Signed in",
  }
  return map[action] ?? action.replaceAll(".", " ")
}
