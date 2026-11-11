import { useEffect, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router-dom"
import {
  ArrowLeft,
  Check,
  ChevronRight,
  Database,
  Eye,
  EyeOff,
  Loader2,
  MoreHorizontal,
  Pencil,
  PlugZap,
  Plus,
  RefreshCw,
  Search,
  Table2,
  Trash2,
  TriangleAlert,
} from "lucide-react"
import { toast } from "sonner"
import { api, ApiError } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import { cn, formatDateTime, relativeTime } from "@/lib/utils"
import type { ConnectorField, ConnectorType, DataSource } from "@/lib/types"
import { Page, PageBody, PageHeader, SourceIcon, StatusBadge, EmptyState, ErrorNotice, sourceMeta, CopyButton } from "@/components/shared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import { Switch } from "@/components/ui/switch"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const categories = [
  { id: "all", label: "All" },
  { id: "sql", label: "SQL" },
  { id: "nosql", label: "NoSQL" },
  { id: "search", label: "Search" },
  { id: "api", label: "APIs" },
  { id: "file", label: "Files" },
]

export function DataSourcesPage() {
  const { tenant, role } = useTenant()
  const queryClient = useQueryClient()
  const canAdmin = role === "owner" || role === "admin"
  const [params, setParams] = useSearchParams()
  const [createOpen, setCreateOpen] = useState(params.get("new") === "1")
  const [editing, setEditing] = useState<DataSource | null>(null)
  const [deleting, setDeleting] = useState<DataSource | null>(null)
  const [schemaFor, setSchemaFor] = useState<DataSource | null>(null)
  const [testingId, setTestingId] = useState<string | null>(null)

  const { data, isLoading } = useQuery({ queryKey: ["datasources", tenant.slug], queryFn: () => api.dataSources(tenant.slug) })
  const { data: types } = useQuery({ queryKey: ["connector-types"], queryFn: () => api.connectorTypes(), staleTime: 300_000 })

  useEffect(() => {
    if (params.get("new") === "1") {
      setCreateOpen(true)
      const next = new URLSearchParams(params)
      next.delete("new")
      setParams(next, { replace: true })
    }
  }, [params, setParams])

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["datasources", tenant.slug] })
    void queryClient.invalidateQueries({ queryKey: ["catalog", tenant.slug] })
    void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
  }

  const testMutation = useMutation({
    mutationFn: (ds: DataSource) => api.testDataSource(tenant.slug, ds.id),
    onMutate: (ds) => setTestingId(ds.id),
    onSettled: () => setTestingId(null),
    onSuccess: (res) => {
      if (res.ok) toast.success(`Connection healthy · ${res.latencyMs} ms`)
      else toast.error(res.error || "Connection failed")
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Connection test failed"),
  })

  const refreshMutation = useMutation({
    mutationFn: (ds: DataSource) => api.refreshDataSource(tenant.slug, ds.id),
    onSuccess: (ds) => {
      refresh()
      if (ds.status === "ready") toast.success(`Schema refreshed · ${ds.tables.length} tables`)
      else toast.error(ds.lastError || "Schema refresh failed")
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Refresh failed"),
  })

  const deleteMutation = useMutation({
    mutationFn: (ds: DataSource) => api.deleteDataSource(tenant.slug, ds.id),
    onSuccess: () => {
      toast.success("Data source removed")
      setDeleting(null)
      refresh()
    },
    onError: (err) => toast.error(err instanceof Error ? err.message : "Could not delete source"),
  })

  const sources = (data?.dataSources ?? []).map((ds) => ({ ...ds, tables: ds.tables ?? [] }))
  const ready = sources.filter((s) => s.status === "ready").length

  return (
    <Page>
      <PageBody>
        <PageHeader
          eyebrow="Data layer"
          title="Data sources"
          description="Connect databases, collections, search indices and APIs. AnyQuery discovers schemas and lets you join them in one query."
          actions={
            <Button onClick={() => setCreateOpen(true)} disabled={!canAdmin} title={canAdmin ? undefined : "Only workspace admins can add data sources"}>
              <Plus className="size-4" /> Add data source
            </Button>
          }
        />

        <div className="mt-5 grid gap-3 sm:grid-cols-3">
          <div className="rounded-lg border bg-card px-4 py-3">
            <div className="text-[11px] text-muted-foreground">Connected</div>
            <div className="mt-1 text-lg font-semibold">{ready} <span className="text-xs font-normal text-muted-foreground">/ {sources.length}</span></div>
          </div>
          <div className="rounded-lg border bg-card px-4 py-3">
            <div className="text-[11px] text-muted-foreground">Tables available</div>
            <div className="mt-1 text-lg font-semibold">{sources.reduce((n, s) => n + s.tables.length, 0)}</div>
          </div>
          <div className="rounded-lg border bg-card px-4 py-3">
            <div className="text-[11px] text-muted-foreground">Connector types</div>
            <div className="mt-1 text-lg font-semibold">{types?.types.length ?? "20+"} <span className="text-xs font-normal text-muted-foreground">built in</span></div>
          </div>
        </div>

        {isLoading ? (
          <div className="mt-5 grid gap-4 md:grid-cols-2">{Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-[190px] rounded-xl" />)}</div>
        ) : sources.length === 0 ? (
          <EmptyState
            icon={Database}
            title="Connect your first data source"
            description="Start with MongoDB, PostgreSQL, SQLite, Snowflake, a REST API or a CSV file. Schema discovery is automatic."
            className="mt-5"
            action={canAdmin ? <Button onClick={() => setCreateOpen(true)}><Plus className="size-4" /> Add data source</Button> : <Badge variant="muted">Ask an admin to connect a source</Badge>}
          />
        ) : (
          <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {sources.map((ds) => (
              <div key={ds.id} className="group relative flex flex-col overflow-hidden rounded-xl border bg-card transition-all hover:shadow-md">
                <div className={cn("h-0.5 w-full", ds.status === "ready" ? "bg-success/70" : "bg-destructive/70")} />
                <div className="flex items-start gap-3 p-4 pb-3">
                  <SourceIcon type={ds.type} size="lg" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <h3 className="truncate text-[14px] font-semibold">{ds.name}</h3>
                      <StatusBadge status={ds.status} />
                    </div>
                    <div className="mt-0.5 text-[11.5px] text-muted-foreground">{ds.typeName}</div>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon-sm" className="text-muted-foreground"><MoreHorizontal className="size-4" /></Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem disabled={!canAdmin} onSelect={() => testMutation.mutate(ds)}>
                        <PlugZap className="size-4" /> Test connection
                      </DropdownMenuItem>
                      <DropdownMenuItem disabled={!canAdmin} onSelect={() => refreshMutation.mutate(ds)}>
                        <RefreshCw className="size-4" /> Refresh schema
                      </DropdownMenuItem>
                      <DropdownMenuItem disabled={!canAdmin} onSelect={() => setEditing(ds)}>
                        <Pencil className="size-4" /> Edit connection
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" disabled={!canAdmin} onSelect={() => setDeleting(ds)}>
                        <Trash2 className="size-4" /> Remove
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                {ds.status === "error" && ds.lastError && (
                  <div className="mx-4 mb-3 flex gap-2 rounded-md border border-destructive/20 bg-destructive/5 px-2.5 py-2 text-[11px] leading-relaxed text-destructive">
                    <TriangleAlert className="mt-0.5 size-3 shrink-0" />
                    <span className="line-clamp-3">{ds.lastError}</span>
                  </div>
                )}

                <div className="flex flex-wrap gap-1.5 px-4">
                  {ds.tables.slice(0, 4).map((t) => (
                    <button key={t.name} onClick={() => setSchemaFor(ds)} className="inline-flex items-center gap-1 rounded-md border bg-muted/30 px-1.5 py-0.5 font-mono text-[10px] hover:border-primary/30 hover:text-primary">
                      {t.name} <span className="text-muted-foreground/60">{t.columns.length}</span>
                    </button>
                  ))}
                  {ds.tables.length > 4 && <button onClick={() => setSchemaFor(ds)} className="rounded-md border px-1.5 py-0.5 text-[10px] text-muted-foreground">+{ds.tables.length - 4}</button>}
                  {ds.tables.length === 0 && <span className="text-[11px] text-muted-foreground">No tables discovered</span>}
                </div>

                <div className="mt-auto flex items-center justify-between gap-2 border-t px-4 py-3 text-[10.5px] text-muted-foreground">
                  <span>{ds.tables.length} tables · synced {relativeTime(ds.lastSyncedAt)}</span>
                  <div className="flex items-center gap-1">
                    {testingId === ds.id || refreshMutation.isPending ? <Loader2 className="size-3 animate-spin" /> : null}
                    <Button variant="ghost" size="xs" onClick={() => setSchemaFor(ds)}><Table2 className="size-3" /> Schema</Button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="mt-7 flex items-start gap-3 rounded-xl border border-dashed bg-muted/20 p-4">
          <SourceIcon type="rest" size="md" />
          <div className="text-[12px] leading-relaxed text-muted-foreground">
            <span className="font-medium text-foreground">Can’t find your system?</span> The connector layer is pluggable. Use the generic REST/JSON or CSV connector for anything with an HTTP endpoint, or add a connector implementing the provider interface.
          </div>
        </div>
      </PageBody>

      <DataSourceDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        types={types?.types ?? []}
        onSaved={(ds) => {
          refresh()
          if (ds.status === "error") toast.warning(`Saved, but connection failed: ${ds.lastError}`)
          else toast.success(`${ds.name} connected · ${ds.tables.length} tables discovered`)
        }}
      />
      <DataSourceDialog
        open={Boolean(editing)}
        onOpenChange={(open) => !open && setEditing(null)}
        types={types?.types ?? []}
        source={editing}
        onSaved={(ds) => {
          setEditing(null)
          refresh()
          if (ds.status === "error") toast.warning(`Saved, but connection failed: ${ds.lastError}`)
          else toast.success("Connection updated")
        }}
      />

      <SchemaDialog source={schemaFor} open={Boolean(schemaFor)} onOpenChange={(open) => !open && setSchemaFor(null)} />

      <AlertDialog open={Boolean(deleting)} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove “{deleting?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              Saved queries that reference this source will stop working. Credentials are deleted with the connection.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={() => deleting && deleteMutation.mutate(deleting)}>
              {deleteMutation.isPending ? "Removing…" : "Remove source"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}

function DataSourceDialog({
  open,
  onOpenChange,
  types,
  source,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  types: ConnectorType[]
  source?: DataSource | null
  onSaved: (ds: DataSource) => void
}) {
  const { tenant } = useTenant()
  const [selected, setSelected] = useState<ConnectorType | null>(null)
  const [search, setSearch] = useState("")
  const [category, setCategory] = useState("all")
  const [name, setName] = useState("")
  const [config, setConfig] = useState<Record<string, string>>({})
  const [showSecrets, setShowSecrets] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const isEdit = Boolean(source)

  useEffect(() => {
    if (!open) return
    setError(null)
    setBusy(false)
    setShowSecrets(false)
    if (source) {
      const spec = types.find((t) => t.type === source.type) ?? null
      setSelected(spec)
      setName(source.name)
      setConfig({ ...source.config })
    } else {
      setSelected(null)
      setName("")
      setConfig({})
      setSearch("")
      setCategory("all")
    }
  }, [open, source, types])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return types.filter(
      (t) =>
        (category === "all" || t.category === category) &&
        (!q || t.name.toLowerCase().includes(q) || t.type.includes(q) || t.description.toLowerCase().includes(q)),
    )
  }, [types, search, category])

  function chooseType(t: ConnectorType) {
    setSelected(t)
    const base = t.name.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").slice(0, 24) || t.type
    setName(base)
    const next: Record<string, string> = {}
    for (const f of t.fields) if (f.default) next[f.key] = f.default
    setConfig(next)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!selected) return
    setBusy(true)
    setError(null)
    try {
      const ds = source
        ? await api.updateDataSource(tenant.slug, source.id, name.trim(), config)
        : await api.createDataSource(tenant.slug, name.trim(), selected.type, config)
      onSaved(ds)
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Could not save data source")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        {!selected ? (
          <>
            <DialogHeader>
              <DialogTitle>Add a data source</DialogTitle>
              <DialogDescription>Choose a connector. Schemas are discovered automatically after you save.</DialogDescription>
            </DialogHeader>
            <div className="mt-4 space-y-3">
              <div className="relative">
                <Search className="absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input autoFocus placeholder="Search connectors…" value={search} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
              </div>
              <div className="flex flex-wrap gap-1">
                {categories.map((c) => (
                  <button key={c.id} onClick={() => setCategory(c.id)} className={cn("rounded-md px-2 py-1 text-[11px] font-medium transition-colors", category === c.id ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted")}>
                    {c.label}
                  </button>
                ))}
              </div>
              <div className="grid max-h-[52vh] gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
                {filtered.map((t) => (
                  <button key={t.type} onClick={() => chooseType(t)} className="group flex items-start gap-3 rounded-lg border bg-card p-3 text-left transition-all hover:border-primary/30 hover:shadow-sm">
                    <SourceIcon type={t.type} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="text-[12.5px] font-medium">{t.name}</span>
                        <ChevronRight className="size-3 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                      </div>
                      <p className="mt-0.5 line-clamp-2 text-[10.5px] leading-relaxed text-muted-foreground">{t.description}</p>
                    </div>
                  </button>
                ))}
                {filtered.length === 0 && <div className="col-span-full py-8 text-center text-sm text-muted-foreground">No connectors match “{search}”.</div>}
              </div>
            </div>
          </>
        ) : (
          <form onSubmit={submit}>
            <DialogHeader>
              <div className="mb-1 flex items-center gap-2.5">
                <SourceIcon type={selected.type} />
                <div>
                  <DialogTitle>{isEdit ? `Edit ${source?.name}` : `Connect ${selected.name}`}</DialogTitle>
                  <DialogDescription className="text-[12px]">{selected.description}</DialogDescription>
                </div>
              </div>
            </DialogHeader>

            {!isEdit && (
              <button type="button" onClick={() => setSelected(null)} className="mt-3 inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
                <ArrowLeft className="size-3" /> Choose another connector
              </button>
            )}

            <div className="mt-4 space-y-3.5">
              <div className="space-y-1.5">
                <Label htmlFor="ds-name">Source name <span className="font-normal text-muted-foreground">(used in SQL)</span></Label>
                <Input id="ds-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. mongo" className="font-mono" disabled={isEdit} />
                {!isEdit && <p className="text-[10.5px] text-muted-foreground">Use a short identifier. Tables are queried as <code className="font-mono">`{name || "source"}`.`table`</code>.</p>}
              </div>
              <div className="grid gap-3.5 sm:grid-cols-2">
                {selected.fields.map((field) => (
                  <ConfigFieldInput
                    key={field.key}
                    field={field}
                    value={config[field.key] ?? ""}
                    onChange={(v) => setConfig((c) => ({ ...c, [field.key]: v }))}
                    showSecrets={showSecrets}
                    className={field.kind === "textarea" ? "sm:col-span-2" : undefined}
                  />
                ))}
              </div>
              {selected.fields.some((f) => f.secret) && (
                <button type="button" onClick={() => setShowSecrets((v) => !v)} className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
                  {showSecrets ? <EyeOff className="size-3" /> : <Eye className="size-3" />} {showSecrets ? "Hide secrets" : "Show secrets"}
                </button>
              )}
              {isEdit && <p className="text-[10.5px] text-muted-foreground">Leaving a masked secret unchanged keeps the stored value.</p>}
              {error && <ErrorNotice message={error} />}
            </div>

            <DialogFooter className="mt-6">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
              <Button type="submit" disabled={busy || !name.trim()}>
                {busy ? <><Loader2 className="size-4 animate-spin" /> Testing connection…</> : <><Check className="size-4" /> {isEdit ? "Save & test" : "Connect source"}</>}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function ConfigFieldInput({
  field,
  value,
  onChange,
  showSecrets,
  className,
}: {
  field: ConnectorField
  value: string
  onChange: (value: string) => void
  showSecrets: boolean
  className?: string
}) {
  if (field.kind === "bool") {
    return (
      <div className={cn("flex items-center justify-between rounded-lg border px-3 py-2", className)}>
        <div>
          <div className="text-[12px] font-medium">{field.label}</div>
          {field.help && <div className="text-[10px] text-muted-foreground">{field.help}</div>}
        </div>
        <Switch checked={value === "true"} onCheckedChange={(checked) => onChange(checked ? "true" : "false")} />
      </div>
    )
  }
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={`field-${field.key}`}>{field.label}{field.required && <span className="text-destructive">*</span>}</Label>
      {field.kind === "select" ? (
        <Select value={value || "__empty"} onValueChange={(v) => onChange(v === "__empty" ? "" : v)}>
          <SelectTrigger id={`field-${field.key}`} className="w-full"><SelectValue placeholder={field.placeholder || "Select"} /></SelectTrigger>
          <SelectContent>
            {field.options?.map((o) => (
              <SelectItem key={o.value || "__empty"} value={o.value || "__empty"}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : field.kind === "textarea" ? (
        <Textarea
          id={`field-${field.key}`}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className={cn("min-h-20 font-mono text-xs", field.secret && !showSecrets && "[-webkit-text-security:disc]")}
        />
      ) : (
        <Input
          id={`field-${field.key}`}
          type={field.kind === "password" || field.kind === "number" ? (field.kind === "number" ? "number" : "text") : "text"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className={cn("font-mono text-xs", field.secret && !showSecrets && "[-webkit-text-security:disc]")}
        />
      )}
      {field.help && <p className="text-[10px] leading-relaxed text-muted-foreground">{field.help}</p>}
    </div>
  )
}

function SchemaDialog({ source, open, onOpenChange }: { source: DataSource | null; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader className="border-b px-5 py-4">
          <div className="flex items-center gap-2.5">
            {source && <SourceIcon type={source.type} />}
            <div>
              <DialogTitle>{source?.name} schema</DialogTitle>
              <DialogDescription>{source?.tables?.length ?? 0} tables · {source?.typeName}</DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="max-h-[65vh] overflow-y-auto px-5 py-4 scrollbar-thin">
          {source?.lastSyncedAt && <p className="mb-3 text-[11px] text-muted-foreground">Last synced {formatDateTime(source.lastSyncedAt)}</p>}
          <div className="space-y-2">
            {(source?.tables ?? []).map((table) => (
              <details key={table.name} className="group rounded-lg border bg-card">
                <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[12.5px] font-medium marker:hidden">
                  <Table2 className="size-3.5 text-muted-foreground" />
                  <span className="font-mono">{table.name}</span>
                  <Badge variant="muted" className="ml-auto text-[9px]">{table.kind}</Badge>
                  <span className="text-[10px] text-muted-foreground">{table.columns.length} cols</span>
                </summary>
                <div className="grid gap-x-4 border-t px-3 py-2 sm:grid-cols-2">
                  {table.columns.map((col) => (
                    <div key={col.name} className="flex items-center justify-between gap-2 py-0.5 text-[11px]">
                      <span className="truncate font-mono">{col.name}</span>
                      <span className="shrink-0 text-[9.5px] text-muted-foreground">{col.type}</span>
                    </div>
                  ))}
                </div>
              </details>
            ))}
            {!(source?.tables ?? []).length && <div className="py-8 text-center text-sm text-muted-foreground">No tables discovered.</div>}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
