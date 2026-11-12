import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { KeyRound, Lock, Plus, RefreshCw, Settings2, ShieldCheck, Trash2, UserCog, Workflow } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { useTenant } from "@/providers/tenant"
import { formatDateTime, relativeTime } from "@/lib/utils"
import { Page, PageBody, PageHeader, RoleBadge, CopyButton, InlineCode } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { ErrorNotice } from "@/components/shared"

export function SettingsPage() {
  const { tenant, role } = useTenant()
  const { config, me, refresh } = useAuth()
  const queryClient = useQueryClient()
  const canAdmin = role === "owner" || role === "admin"
  const [tab, setTab] = useState("general")
  const [name, setName] = useState(tenant.name)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newKeyOpen, setNewKeyOpen] = useState(false)
  const [revokeKey, setRevokeKey] = useState<{ id: string; name: string } | null>(null)

  const { data: keys, isLoading: keysLoading } = useQuery({ queryKey: ["apikeys", tenant.slug], queryFn: () => api.apiKeys(tenant.slug), enabled: canAdmin })
  const keyMutation = useMutation({
    mutationFn: (id: string) => api.revokeApiKey(tenant.slug, id),
    onSuccess: () => {
      toast.success("API key revoked")
      setRevokeKey(null)
      void queryClient.invalidateQueries({ queryKey: ["apikeys", tenant.slug] })
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not revoke key"),
  })

  useEffect(() => setName(tenant.name), [tenant.name])

  async function saveName(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError(null)
    try {
      await api.updateTenant(tenant.slug, name.trim())
      await refresh()
      await queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
      toast.success("Workspace updated")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not update workspace")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Page>
      <PageBody>
        <PageHeader eyebrow="Workspace" title="Settings" description="Manage workspace identity, API access and security defaults." />

        <Tabs value={tab} onValueChange={setTab} className="mt-5">
          <TabsList>
            <TabsTrigger value="general"><Settings2 className="size-3.5" /> General</TabsTrigger>
            {canAdmin && <TabsTrigger value="api-keys"><KeyRound className="size-3.5" /> API keys</TabsTrigger>}
            <TabsTrigger value="security"><ShieldCheck className="size-3.5" /> Security</TabsTrigger>
          </TabsList>

          <TabsContent value="general">
            <div className="grid gap-5 lg:grid-cols-[1.5fr_1fr]">
              <div className="surface">
                <div className="px-5 pt-5 pb-2">
                  <h2 className="text-sm font-semibold">Workspace profile</h2>
                  <p className="mt-0.5 text-xs text-muted-foreground">Visible to all members</p>
                </div>
                <form onSubmit={saveName} className="space-y-4 p-5">
                  <div className="space-y-1.5">
                    <Label htmlFor="ws-name">Name</Label>
                    <Input id="ws-name" value={name} onChange={(e) => setName(e.target.value)} disabled={!canAdmin} />
                  </div>
                  <div className="space-y-1.5">
                    <Label>Workspace URL</Label>
                    <div className="flex h-9 items-center rounded-lg border bg-muted/40 px-3 font-mono text-[11.5px] text-muted-foreground">/w/{tenant.slug}</div>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <ReadOnly label="Workspace ID" value={tenant.id} mono />
                    <ReadOnly label="Created" value={formatDateTime(tenant.createdAt)} />
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <ReadOnly label="Your role" value={<RoleBadge role={role} />} />
                    <ReadOnly label="Membership" value={me?.user.email ?? "—"} />
                  </div>
                  {error && <ErrorNotice message={error} />}
                  {canAdmin && (
                    <div className="flex justify-end pt-1">
                      <Button type="submit" disabled={saving || !name.trim() || name.trim() === tenant.name}>{saving ? "Saving…" : "Save changes"}</Button>
                    </div>
                  )}
                </form>
              </div>

              <div className="space-y-4">
                <div className="surface p-5">
                  <div className="flex items-center gap-2 text-sm font-semibold"><Workflow className="size-4 text-primary" /> Query limits</div>
                  <div className="mt-4 space-y-3 text-[12px]">
                    <LimitRow label="Query timeout" value="30 seconds" />
                    <LimitRow label="Result rows" value="10,000" />
                    <LimitRow label="Source rows" value="100,000 per table" />
                    <LimitRow label="Join guard" value="2,000,000 rows" />
                  </div>
                  <p className="mt-4 text-[10.5px] leading-relaxed text-muted-foreground">Configured by the server administrator. Add filters and LIMIT clauses for large tables.</p>
                </div>
                <div className="surface p-5">
                  <div className="flex items-center gap-2 text-sm font-semibold"><UserCog className="size-4 text-primary" /> Session</div>
                  <div className="mt-3 text-[12px] leading-relaxed text-muted-foreground">
                    Sessions are server-side and expire after <strong className="text-foreground">{config?.sessionHours ?? 168} hours</strong>. Signing out revokes this browser session.
                  </div>
                </div>
              </div>
            </div>
          </TabsContent>

          {canAdmin && (
            <TabsContent value="api-keys">
              <div className="overflow-hidden surface">
                <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-5 pb-2">
                  <div>
                    <h2 className="text-sm font-semibold">Workspace API keys</h2>
                    <p className="mt-0.5 text-xs text-muted-foreground">Execute queries from scripts, notebooks or agents.</p>
                  </div>
                  <Button size="sm" onClick={() => setNewKeyOpen(true)}><Plus className="size-3.5" /> Create key</Button>
                </div>
                {keysLoading ? (
                  <div className="space-y-2 p-4">{Array.from({ length: 2 }).map((_, i) => <Skeleton key={i} className="h-12" />)}</div>
                ) : !(keys?.apiKeys ?? []).length ? (
                  <div className="px-5 py-10 text-center">
                    <KeyRound className="mx-auto size-5 text-muted-foreground/50" />
                    <p className="mt-2 text-sm font-medium">No API keys</p>
                    <p className="mt-1 text-xs text-muted-foreground">Create a key to query this workspace programmatically.</p>
                  </div>
                ) : (
                  <div className="space-y-0.5 p-1.5">
                    {(keys?.apiKeys ?? []).map((key) => (
                      <div key={key.id} className="flex items-center gap-3 px-5 py-3.5">
                        <div className="grid size-8 place-items-center rounded-lg bg-muted text-muted-foreground"><KeyRound className="size-3.5" /></div>
                        <div className="min-w-0 flex-1">
                          <div className="text-[12.5px] font-medium">{key.name}</div>
                          <div className="mt-0.5 flex flex-wrap items-center gap-2 text-[10.5px] text-muted-foreground">
                            <code className="rounded border bg-muted/50 px-1 py-0.5 font-mono">{key.prefix}••••</code>
                            <span>created {relativeTime(key.createdAt)}</span>
                            <span>·</span>
                            <span>{key.lastUsedAt ? `last used ${relativeTime(key.lastUsedAt)}` : "never used"}</span>
                          </div>
                        </div>
                        <Button variant="ghost" size="icon-sm" className="text-muted-foreground hover:text-destructive" onClick={() => setRevokeKey({ id: key.id, name: key.name })}>
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="mt-4 rounded-xl border bg-[oklch(0.18_0.025_268)] p-5 text-white">
                <div className="mb-2 flex items-center gap-2 text-[12px] font-medium"><Lock className="size-3.5" /> Example request</div>
                <pre className="overflow-x-auto font-mono text-[10.5px] leading-relaxed text-white/65">{`curl -X POST ${window.location.origin}/api/v1/tenants/${tenant.slug}/queries/execute \\
  -H "Authorization: Bearer aq_…" \\
  -H "Content-Type: application/json" \\
  -d '{"sql":"SELECT * FROM \`mongo\`.\`orders\` LIMIT 10"}'`}</pre>
              </div>
            </TabsContent>
          )}

          <TabsContent value="security">
            <div className="grid gap-4 md:grid-cols-2">
              <SecurityCard icon={ShieldCheck} title="Single sign-on" body={config?.oidcEnabled ? `OIDC is enabled via ${config.issuer}.` : "OIDC is not configured on this server. Development login is available in non-production environments."} badge={config?.oidcEnabled ? "Enabled" : "Not configured"} />
              <SecurityCard icon={Lock} title="Credential encryption" body="Data source credentials are encrypted at rest with AES-256-GCM. Secrets are never returned to the browser." badge="AES-256-GCM" />
              <SecurityCard icon={KeyRound} title="Sessions" body="Opaque session tokens are stored as SHA-256 hashes; cookies are HttpOnly, SameSite=Lax and Secure over HTTPS." badge="HttpOnly" />
              <SecurityCard icon={Workflow} title="Query isolation" body="Every query is scoped to the workspace membership. API keys are bound to a single workspace." badge="Tenant-scoped" />
            </div>
            <div className="mt-5 surface p-5">
              <h2 className="text-sm font-semibold">Cross-origin protection</h2>
              <p className="mt-1 max-w-2xl text-[12px] leading-relaxed text-muted-foreground">
                Mutating requests from cookie-authenticated browsers validate the <InlineCode>Origin</InlineCode> header. API keys bypass CSRF checks by design and should be kept secret.
              </p>
            </div>
          </TabsContent>
        </Tabs>
      </PageBody>

      <CreateKeyDialog open={newKeyOpen} onOpenChange={setNewKeyOpen} />
      <AlertDialog open={Boolean(revokeKey)} onOpenChange={(open) => !open && setRevokeKey(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke “{revokeKey?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Any scripts using this key will stop working immediately. This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={() => revokeKey && keyMutation.mutate(revokeKey.id)}>
              {keyMutation.isPending ? "Revoking…" : "Revoke key"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}

function ReadOnly({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="space-y-1.5">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className={mono ? "flex h-9 items-center truncate rounded-lg border bg-muted/30 px-3 font-mono text-[11px]" : "flex h-9 items-center rounded-lg border bg-muted/30 px-3 text-[12px]"}>{value}</div>
    </div>
  )
}

function LimitRow({ label, value }: { label: string; value: string }) {
  return <div className="flex items-center justify-between"><span className="text-muted-foreground">{label}</span><span className="font-medium">{value}</span></div>
}

function SecurityCard({ icon: Icon, title, body, badge }: { icon: typeof Lock; title: string; body: string; badge: string }) {
  return (
    <div className="surface p-5">
      <div className="flex items-start justify-between">
        <div className="grid size-8 place-items-center rounded-lg bg-primary/8 text-primary"><Icon className="size-4" /></div>
        <Badge variant="outline" className="text-[9.5px]">{badge}</Badge>
      </div>
      <div className="mt-3 text-[13px] font-semibold">{title}</div>
      <p className="mt-1 text-[11.5px] leading-relaxed text-muted-foreground">{body}</p>
    </div>
  )
}

function CreateKeyDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { tenant } = useTenant()
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [raw, setRaw] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await api.createApiKey(tenant.slug, name.trim())
      setRaw(res.key)
      void queryClient.invalidateQueries({ queryKey: ["apikeys", tenant.slug] })
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create API key")
    } finally {
      setBusy(false)
    }
  }

  function close() {
    setName("")
    setRaw(null)
    setError(null)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent>
        {raw ? (
          <>
            <DialogHeader>
              <DialogTitle>API key created</DialogTitle>
              <DialogDescription>Copy this key now. For security, it will not be shown again.</DialogDescription>
            </DialogHeader>
            <div className="mt-4 rounded-lg border bg-muted/40 p-3">
              <code className="block break-all font-mono text-[11px]">{raw}</code>
            </div>
            <div className="mt-2 flex justify-end"><CopyButton value={raw} label="Copy key" /></div>
            <DialogFooter className="mt-4"><Button onClick={close}>Done</Button></DialogFooter>
          </>
        ) : (
          <form onSubmit={submit}>
            <DialogHeader>
              <DialogTitle>Create API key</DialogTitle>
              <DialogDescription>Give the key a descriptive name so teammates know where it’s used.</DialogDescription>
            </DialogHeader>
            <div className="mt-5 space-y-1.5">
              <Label htmlFor="key-name">Key name</Label>
              <Input id="key-name" autoFocus placeholder="e.g. Warehouse sync" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            {error && <div className="mt-3"><ErrorNotice message={error} /></div>}
            <DialogFooter className="mt-6">
              <Button type="button" variant="outline" onClick={close}>Cancel</Button>
              <Button type="submit" disabled={busy || !name.trim()}>{busy ? "Creating…" : "Create key"}</Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
