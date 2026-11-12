import { useEffect, useMemo, useRef, useState } from "react"
import { ChevronRight, Columns3, Database, PanelLeftClose, Plus, RefreshCw, Search, Table2, TriangleAlert } from "lucide-react"
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu"
import { cn } from "@/lib/utils"
import { SourceIcon } from "@/components/SourceIcon"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Skeleton } from "@/components/ui/skeleton"

export interface CatalogSource {
  name: string
  type: string
  icon?: string
  status: string
  error?: string
  tables: { name: string; displayName?: string; kind: string; columns: { name: string; type: string }[] }[]
}

export function SchemaExplorer({
  sources,
  loading,
  onInsert,
  onRefresh,
  onAddSource,
  onManage,
  onCollapse,
}: {
  sources: CatalogSource[]
  loading?: boolean
  onInsert: (text: string) => void
  onRefresh?: () => void
  onAddSource: () => void
  onManage: () => void
  onCollapse?: () => void
}) {
  const [query, setQuery] = useState("")
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [openTables, setOpenTables] = useState<Record<string, boolean>>({})
  const clickTimers = useRef<Record<string, number>>({})

  useEffect(() => () => {
    for (const timer of Object.values(clickTimers.current)) window.clearTimeout(timer)
  }, [])

  function toggleTable(key: string) {
    setOpenTables((o) => ({ ...o, [key]: !o[key] }))
  }

  function handleTableClick(key: string) {
    if (clickTimers.current[key]) {
      window.clearTimeout(clickTimers.current[key])
      delete clickTimers.current[key]
      return
    }
    clickTimers.current[key] = window.setTimeout(() => {
      delete clickTimers.current[key]
      toggleTable(key)
    }, 180)
  }

  function insertTable(source: string, table: string, key: string) {
    if (clickTimers.current[key]) {
      window.clearTimeout(clickTimers.current[key])
      delete clickTimers.current[key]
    }
    onInsert(tableRef(source, table))
  }

  const filtered = useMemo(() => {
    const normalized = sources.map((s) => ({ ...s, tables: s.tables ?? [] }))
    const q = query.trim().toLowerCase()
    if (!q) return normalized
    return normalized
      .map((source) => ({
        ...source,
        tables: source.tables.filter(
          (t) =>
            t.name.toLowerCase().includes(q) ||
            t.columns.some((c) => c.name.toLowerCase().includes(q)),
        ),
      }))
      .filter((source) => source.name.toLowerCase().includes(q) || source.tables.length > 0)
  }, [sources, query])

  const tableRef = (source: string, table: string) => `\`${source}\`.\`${table}\``

  return (
    <div className="flex h-full flex-col bg-sidebar/45">
      <div className="flex h-11 items-center justify-between border-b border-border/45 px-4">
        <div className="flex items-center gap-1.5 text-[11px] font-medium tracking-[0.12em] text-muted-foreground uppercase">Schema</div>
        <div className="flex items-center gap-0.5">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-xs" onClick={onRefresh} aria-label="Refresh schema"><RefreshCw className="size-3.5" /></Button>
            </TooltipTrigger>
            <TooltipContent>Refresh schemas</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-xs" onClick={onAddSource} aria-label="Add data source"><Plus className="size-3.5" /></Button>
            </TooltipTrigger>
            <TooltipContent>Add data source</TooltipContent>
          </Tooltip>
          {onCollapse && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-xs" onClick={onCollapse} aria-label="Collapse schema"><PanelLeftClose className="size-3.5" /></Button>
              </TooltipTrigger>
              <TooltipContent>Collapse schema</TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
      <div className="border-b border-border/45 px-3 pb-2.5">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input placeholder="Search tables & columns" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 bg-background pl-8 text-xs" />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-2 py-2 scrollbar-thin">
        {loading ? (
          <div className="space-y-2 p-1">{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-7" />)}</div>
        ) : filtered.length === 0 ? (
          <div className="px-3 py-8 text-center">
            <Database className="mx-auto size-6 text-muted-foreground/50" />
            <p className="mt-2 text-xs text-muted-foreground">{query ? "No matching tables" : "No data sources yet"}</p>
            <Button size="sm" variant="outline" className="mt-3" onClick={onAddSource}><Plus className="size-3.5" /> Connect data</Button>
          </div>
        ) : (
          filtered.map((source) => {
            const isCollapsed = collapsed[source.name] && !query
            return (
              <div key={source.name} className="mb-1">
                <button
                  className="group flex w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left transition-colors hover:bg-accent/60"
                  onClick={() => setCollapsed((c) => ({ ...c, [source.name]: !c[source.name] }))}
                >
                  <ChevronRight className={cn("size-3 shrink-0 text-muted-foreground transition-transform", !isCollapsed && "rotate-90")} />
                  <SourceIcon type={source.type} icon={source.icon} size="sm" className="size-6 rounded" />
                  <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium">{source.name}</span>
                  {source.status !== "ready" && (
                    <Tooltip>
                      <TooltipTrigger asChild><TriangleAlert className="size-3 text-warning" /></TooltipTrigger>
                      <TooltipContent>{source.error || "Schema unavailable"}</TooltipContent>
                    </Tooltip>
                  )}
                  <span className="text-[10px] text-muted-foreground">{source.tables.length}</span>
                </button>
                {!isCollapsed && (
                  <div className="ml-3 border-l pl-2">
                    {source.tables.length === 0 && <div className="px-2 py-2 text-[11px] text-muted-foreground">No tables</div>}
                    {source.tables.map((table) => {
                      const key = `${source.name}.${table.name}`
                      const open = openTables[key] || Boolean(query)
                      return (
                        <div key={key} className="group/row">
                          <ContextMenu>
                            <ContextMenuTrigger asChild>
                              <button
                                className="flex w-full items-center gap-1.5 rounded py-1 pr-1.5 pl-1.5 text-left transition-colors hover:bg-accent/60"
                                onClick={() => handleTableClick(key)}
                                onDoubleClick={() => insertTable(source.name, table.name, key)}
                                title={`Double-click to insert ${tableRef(source.name, table.name)}`}
                              >
                                <ChevronRight className={cn("size-2.5 shrink-0 text-muted-foreground/70 transition-transform", open && "rotate-90")} />
                                {table.kind === "collection" ? <Database className="size-3 shrink-0 text-muted-foreground" /> : <Table2 className="size-3 shrink-0 text-muted-foreground" />}
                                <span className="min-w-0 flex-1 truncate font-mono text-[11px]">{table.name}</span>
                                <span className="text-[9px] text-muted-foreground">{table.columns.length}</span>
                              </button>
                            </ContextMenuTrigger>
                            <ContextMenuContent>
                              <ContextMenuLabel>{table.name}</ContextMenuLabel>
                              <ContextMenuItem onSelect={() => onInsert(tableRef(source.name, table.name))}>
                                <Plus /> Insert table reference
                              </ContextMenuItem>
                              <ContextMenuItem onSelect={() => toggleTable(key)}>
                                <ChevronRight /> {open ? "Hide columns" : "Show columns"}
                              </ContextMenuItem>
                              <ContextMenuSeparator />
                              <ContextMenuItem onSelect={() => void navigator.clipboard.writeText(tableRef(source.name, table.name))}>
                                Copy qualified name
                              </ContextMenuItem>
                            </ContextMenuContent>
                          </ContextMenu>
                          {open && (
                            <div className="mb-1 ml-3 border-l pl-2">
                              {table.columns.map((col) => (
                                <ContextMenu key={col.name}>
                                  <ContextMenuTrigger asChild>
                                    <button
                                      onDoubleClick={() => onInsert(col.name)}
                                      className="flex w-full items-center gap-1.5 rounded px-1.5 py-[3px] text-left transition-colors hover:bg-accent/60"
                                      title={`Double-click to insert ${col.name} (${col.type})`}
                                    >
                                      <Columns3 className="size-2.5 shrink-0 text-muted-foreground/50" />
                                      <span className="min-w-0 flex-1 truncate font-mono text-[10.5px]">{col.name}</span>
                                      <span className="text-[8.5px] text-muted-foreground/70">{col.type}</span>
                                    </button>
                                  </ContextMenuTrigger>
                                  <ContextMenuContent>
                                    <ContextMenuLabel>{table.name}.{col.name}</ContextMenuLabel>
                                    <ContextMenuItem onSelect={() => onInsert(col.name)}>
                                      <Plus /> Insert column
                                    </ContextMenuItem>
                                    <ContextMenuItem onSelect={() => void navigator.clipboard.writeText(col.name)}>
                                      Copy column name
                                    </ContextMenuItem>
                                  </ContextMenuContent>
                                </ContextMenu>
                              ))}
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })
        )}
      </div>
      <div className="border-t border-border/45 px-3 py-3">
        <div className="mb-2 px-1 text-[9.5px] text-muted-foreground/65">Double-click to insert · right-click for actions</div>
        <Button variant="ghost" size="xs" className="w-full justify-between text-muted-foreground" onClick={onManage}>
          Manage sources <Badge variant="muted" className="text-[9px]">{sources.length}</Badge>
        </Button>
      </div>
    </div>
  )
}
