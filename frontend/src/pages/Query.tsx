import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import CodeMirror, { type ReactCodeMirrorRef } from "@uiw/react-codemirror"
import { sql, StandardSQL } from "@codemirror/lang-sql"
import { keymap } from "@codemirror/view"
import {
  Bookmark,
  Braces,
  CheckCircle2,
  ChevronDown,
  CircleAlert,
  Clock3,
  Copy,
  Database,
  Download,
  FileCode2,
  History,
  Loader2,
  PanelLeft,
  Play,
  RefreshCw,
  Save,
  Sparkles,
  SquareTerminal,
  Trash2,
  WandSparkles,
} from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import { useI18n } from "@/i18n"
import { useTheme } from "@/providers/theme"
import { cn, downloadCSV, formatDuration, formatNumber, relativeTime, truncate } from "@/lib/utils"
import type { ExecuteResponse, QueryRun, QueryResult, SavedQuery } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { SchemaExplorer, type CatalogSource } from "@/components/query/SchemaExplorer"
import { ResultsTable } from "@/components/query/ResultsTable"
import { SaveQueryDialog } from "@/components/query/SaveQueryDialog"
import { ErrorNotice } from "@/components/shared"

export function QueryPage() {
  const { t } = useI18n()
  const { tenant, role } = useTenant()
  const { resolvedTheme } = useTheme()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [params, setParams] = useSearchParams()
  const savedId = params.get("saved") || ""
  const editorRef = useRef<ReactCodeMirrorRef>(null)
  const runRef = useRef<(background?: boolean) => void>(() => {})
  const canEdit = role === "owner" || role === "admin" || role === "member"

  const { data: catalog, refetch: refetchCatalog, isLoading: catalogLoading } = useQuery({
    queryKey: ["catalog", tenant.slug],
    queryFn: () => api.catalog(tenant.slug),
  })
  const sources: CatalogSource[] = (catalog?.sources ?? []).map((s) => ({ ...s, icon: s.icon, tables: (s.tables ?? []).map((t) => ({ ...t, columns: t.columns ?? [] })) }))

  const { data: runsData } = useQuery({
    queryKey: ["runs", tenant.slug, "studio"],
    queryFn: () => api.runs(tenant.slug, 100),
  })
  const { data: saved } = useQuery({
    queryKey: ["saved-query", tenant.slug, savedId],
    queryFn: () => api.savedQuery(tenant.slug, savedId),
    enabled: Boolean(savedId),
  })

  const draftKey = `aq-draft-${tenant.id}`
  const [sqlText, setSQLText] = useState(() => localStorage.getItem(draftKey) || "")
  const [currentSaved, setCurrentSaved] = useState<SavedQuery | null>(null)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [lastRun, setLastRun] = useState<QueryRun | null>(null)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  const [errorHint, setErrorHint] = useState<string | null>(null)
  const [saveOpen, setSaveOpen] = useState(false)
  const [schemaOpen, setSchemaOpen] = useState(false)
  const [paramsOpen, setParamsOpen] = useState(false)
  const [paramText, setParamText] = useState(() => localStorage.getItem(`aq-params-${tenant.id}`) || "")
  const [tab, setTab] = useState("results")
  const [historyPage, setHistoryPage] = useState(0)
  const [schemaCollapsed, setSchemaCollapsed] = useState(() => localStorage.getItem("aq-schema-collapsed") === "1")
  const [schemaWidth, setSchemaWidth] = useState(() => {
    const saved = Number(localStorage.getItem("aq-schema-width"))
    return Number.isFinite(saved) && saved >= 220 && saved <= 520 ? saved : 268
  })
  const [editorRatio, setEditorRatio] = useState(() => {
    const saved = Number(localStorage.getItem("aq-editor-ratio"))
    return Number.isFinite(saved) && saved >= 0.25 && saved <= 0.78 ? saved : 0.43
  })
  const [autoRefreshMs, setAutoRefreshMs] = useState(() => {
    const saved = Number(localStorage.getItem("aq-auto-refresh-ms"))
    return [0, 15000, 30000, 60000, 300000].includes(saved) ? saved : 0
  })
  const runningRef = useRef(false)
  runningRef.current = running

  useEffect(() => {
    localStorage.setItem("aq-schema-collapsed", schemaCollapsed ? "1" : "0")
  }, [schemaCollapsed])

  useEffect(() => {
    localStorage.setItem("aq-schema-width", String(Math.round(schemaWidth)))
  }, [schemaWidth])

  useEffect(() => {
    localStorage.setItem("aq-editor-ratio", String(editorRatio))
    editorRef.current?.view?.requestMeasure()
  }, [editorRatio])

  useEffect(() => {
    localStorage.setItem("aq-auto-refresh-ms", String(autoRefreshMs))
  }, [autoRefreshMs])

  useEffect(() => {
    localStorage.setItem(`aq-params-${tenant.id}`, paramText)
  }, [paramText, tenant.id])

  useEffect(() => {
    if (!autoRefreshMs) return
    const tick = () => {
      if (document.visibilityState === "visible" && !runningRef.current) {
        runRef.current(true)
      }
    }
    const timer = window.setInterval(tick, autoRefreshMs)
    const onVisibility = () => {
      if (document.visibilityState === "visible") tick()
    }
    document.addEventListener("visibilitychange", onVisibility)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener("visibilitychange", onVisibility)
    }
  }, [autoRefreshMs])

  // Restore a saved query from the URL.
  useEffect(() => {
    if (saved && saved.id === savedId) {
      setCurrentSaved(saved)
      setSQLText(saved.sql)
      localStorage.setItem(draftKey, saved.sql)
    }
  }, [saved, savedId, draftKey])

  useEffect(() => {
    if (sqlText) localStorage.setItem(draftKey, sqlText)
  }, [sqlText, draftKey])

  // Seed a helpful starting query when the workspace is empty.
  useEffect(() => {
    if (sqlText.trim() || savedId || !sources.length) return
    const source = sources.find((s) => s.status === "ready" && s.tables.length)
    if (!source) return
    const table = source.tables[0]
    const starter = `-- Federated SQL: reference any connected source as \`source\`.\`table\`\nSELECT *\nFROM \`${source.name}\`.\`${table.name}\`\nLIMIT 100`
    setSQLText(starter)
  }, [sources, sqlText, savedId])

  const completionSchema = useMemo(() => {
    const out: Record<string, Record<string, string[]>> = {}
    for (const source of sources) {
      out[source.name] = {}
      for (const table of source.tables) {
        out[source.name][table.name] = table.columns.map((c) => c.name)
      }
    }
    return out
  }, [sources])

  const runQuery = useCallback(async (background = false) => {
    const text = sqlText.trim()
    if (!text || running) return
    setRunning(true)
    setError(null)
    setErrorCode(null)
    setErrorHint(null)
    setLastRun(null)
    setTab("results")
    let bindParams: unknown[] = []
    if (paramText.trim()) {
      try {
        const parsed = JSON.parse(paramText)
        if (!Array.isArray(parsed)) throw new Error("Parameters must be a JSON array")
        bindParams = parsed
      } catch (err) {
        setError(err instanceof Error ? err.message : "Invalid parameter JSON")
        setErrorCode("parameter_error")
        setErrorHint("Enter a JSON array, for example [42, \"paid\"]")
        setRunning(false)
        return
      }
    }
    try {
      const payload: ExecuteResponse = await api.execute(tenant.slug, text, currentSaved?.id, bindParams)
      if (payload.error) {
        setError(payload.error.message)
        setErrorCode(payload.error.code)
        setErrorHint(payload.error.hint || null)
        setResult(null)
      } else if (payload.result) {
        setResult(payload.result)
      }
      if (payload.run) setLastRun(payload.run)
      if (!background) {
        void queryClient.invalidateQueries({ queryKey: ["runs", tenant.slug] })
        void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
      }
      if (currentSaved) void queryClient.invalidateQueries({ queryKey: ["saved-query", tenant.slug, currentSaved.id] })
    } catch (err) {
      setError(err instanceof Error ? err.message : "Query failed")
      setErrorCode("network_error")
      setResult(null)
    } finally {
      setRunning(false)
    }
  }, [sqlText, running, tenant.slug, currentSaved, queryClient])

  runRef.current = runQuery

  const extensions = useMemo(
    () => [
      sql({ dialect: StandardSQL, schema: completionSchema, upperCaseKeywords: true }),
      keymap.of([
        {
          key: "Mod-Enter",
          run: () => {
            runRef.current()
            return true
          },
        },
      ]),
    ],
    [completionSchema],
  )

  function insertText(text: string) {
    const view = editorRef.current?.view
    if (!view) {
      setSQLText((s) => `${s}${s && !s.endsWith(" ") ? " " : ""}${text}`)
      return
    }
    view.dispatch(view.state.replaceSelection(text))
    view.focus()
    setSchemaOpen(false)
  }

  function newQuery() {
    setSQLText("")
    setCurrentSaved(null)
    setResult(null)
    setError(null)
    setLastRun(null)
    setParams({})
    localStorage.removeItem(draftKey)
    editorRef.current?.view?.focus()
  }

  function loadRun(run: QueryRun) {
    setSQLText(run.sql)
    setCurrentSaved(null)
    setParams({})
    setTab("results")
  }

  async function formatQuery() {
    if (!sqlText.trim()) return
    if (sqlText.includes("|>")) {
      toast.info("Pipeline queries are already readable — automatic formatting supports classic SQL.")
      return
    }
    try {
      const { format } = await import("sql-formatter")
      setSQLText(format(sqlText, { language: "sql", keywordCase: "upper", tabWidth: 2 }))
    } catch {
      toast.error("Could not format this query")
    }
  }

  function beginSchemaResize(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault()
    const startX = event.clientX
    const startWidth = schemaWidth
    const maxWidth = Math.min(520, Math.max(260, window.innerWidth - 520))
    document.body.style.userSelect = "none"
    document.body.style.cursor = "col-resize"
    const move = (ev: PointerEvent) => {
      setSchemaWidth(Math.min(maxWidth, Math.max(220, startWidth + ev.clientX - startX)))
    }
    const up = () => {
      document.removeEventListener("pointermove", move)
      document.removeEventListener("pointerup", up)
      document.body.style.userSelect = ""
      document.body.style.cursor = ""
    }
    document.addEventListener("pointermove", move)
    document.addEventListener("pointerup", up)
  }

  function beginEditorResize(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault()
    const container = event.currentTarget.parentElement
    if (!container) return
    const rect = container.getBoundingClientRect()
    const startY = event.clientY
    const startRatio = editorRatio
    document.body.style.userSelect = "none"
    document.body.style.cursor = "row-resize"
    const move = (ev: PointerEvent) => {
      setEditorRatio(Math.min(0.78, Math.max(0.25, startRatio + (ev.clientY - startY) / rect.height)))
    }
    const up = () => {
      document.removeEventListener("pointermove", move)
      document.removeEventListener("pointerup", up)
      document.body.style.userSelect = ""
      document.body.style.cursor = ""
      editorRef.current?.view?.requestMeasure()
    }
    document.addEventListener("pointermove", move)
    document.addEventListener("pointerup", up)
  }

  const autoLabel = autoRefreshMs === 15000 ? "15s" : autoRefreshMs === 30000 ? "30s" : autoRefreshMs === 60000 ? "1m" : autoRefreshMs === 300000 ? "5m" : ""
  const sourceCount = sources.length
  const tableCount = sources.reduce((n, s) => n + s.tables.length, 0)
  const runs = runsData?.runs ?? []
  const historyPageSize = 20
  const historyPageCount = Math.max(1, Math.ceil(runs.length / historyPageSize))
  const currentHistoryPage = Math.min(historyPage, historyPageCount - 1)
  const pageRuns = runs.slice(currentHistoryPage * historyPageSize, (currentHistoryPage + 1) * historyPageSize)

  const renderSchema = (onCollapse: () => void) => (
    <SchemaExplorer
      sources={sources}
      loading={catalogLoading}
      onInsert={insertText}
      onRefresh={() => void refetchCatalog()}
      onAddSource={() => navigate(`/w/${tenant.slug}/sources?new=1`)}
      onManage={() => navigate(`/w/${tenant.slug}/sources`)}
      onCollapse={onCollapse}
    />
  )

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-[52px] shrink-0 items-center gap-2 border-b border-border/45 bg-background px-3 md:px-4">
        <div className="flex min-w-0 items-center gap-2">
          <div className="hidden size-7 place-items-center rounded-md bg-primary/10 text-primary sm:grid">
            <SquareTerminal className="size-3.5" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-[13px] font-semibold">{currentSaved?.name || t("Untitled query")}</span>
              {currentSaved && <Badge variant="outline" className="hidden text-[9.5px] capitalize sm:inline-flex">{currentSaved.visibility}</Badge>}
              {currentSaved && <CheckCircle2 className="hidden size-3 text-success sm:block" />}
            </div>
            <div className="hidden text-[10.5px] text-muted-foreground sm:block">
              {sourceCount} sources · {tableCount} tables · {tenant.name}
            </div>
          </div>
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="sm" className="hidden md:inline-flex" onClick={newQuery}><FileCode2 className="size-3.5" /> {t("New")}</Button>
          <Button variant="outline" size="sm" className="hidden sm:inline-flex" onClick={() => void formatQuery()} title="Format SQL"><WandSparkles className="size-3.5" /> {t("Format")}</Button>
          <Button
            variant="outline"
            size="sm"
            className={schemaCollapsed ? "inline-flex" : "lg:hidden"}
            onClick={() => {
              if (window.innerWidth >= 1024) setSchemaCollapsed(false)
              else setSchemaOpen(true)
            }}
          >
            <PanelLeft className="size-3.5" /> {t("Schema")}
          </Button>
          {canEdit && (
            <Button variant="outline" size="sm" onClick={() => setSaveOpen(true)} disabled={!sqlText.trim()}>
              <Save className="size-3.5" /> <span className="hidden sm:inline">{currentSaved ? t("Update") : t("Save")}</span>
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => setParamsOpen(true)}>
            <Braces className="size-3.5" /> <span className="hidden sm:inline">{t("Params")}</span>
          </Button>
          <Button size="sm" onClick={() => void runQuery()} disabled={running || !sqlText.trim()} className="min-w-[82px]">
            {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5 fill-current" />}
            {running ? t("Running") : t("Run")}
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 gap-2 p-2 pt-0">
        {!schemaCollapsed && (
          <>
            <aside style={{ width: schemaWidth }} className="hidden shrink-0 overflow-hidden surface lg:block">
              {renderSchema(() => setSchemaCollapsed(true))}
            </aside>
            <div
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize schema panel"
              onPointerDown={beginSchemaResize}
              onDoubleClick={() => setSchemaWidth(268)}
              className="group relative hidden w-0 cursor-col-resize touch-none lg:block"
            >
              <span className="absolute inset-y-0 -left-1.5 w-3" />
              <span className="pointer-events-none absolute inset-y-2 -left-px w-px bg-border transition-colors group-hover:bg-primary/60" />
            </div>
          </>
        )}

        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div style={{ height: `${editorRatio * 100}%` }} className="flex min-h-[150px] flex-col overflow-hidden surface">
            <div className="flex h-9 shrink-0 items-center justify-between border-b border-border/45 px-3 text-[11px] text-muted-foreground">
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center gap-1 font-mono text-[10px]"><span className="size-1.5 rounded-full bg-emerald-500" /> SQL</span>
                {currentSaved && <span className="hidden items-center gap-1 sm:inline-flex"><Bookmark className="size-3" /> {t("saved")}</span>}
              </div>
              <div className="flex items-center gap-2 text-[10px]">
                <span className="hidden items-center gap-1 sm:inline-flex"><Sparkles className="size-3" /> Cmd/Ctrl + Enter to run</span>
                <button className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-accent" onClick={() => setTab("history")}><History className="size-3" /> {t("History")}</button>
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-hidden bg-card">
              <CodeMirror
                ref={editorRef}
                value={sqlText}
                onChange={setSQLText}
                height="100%"
                theme={resolvedTheme}
                extensions={extensions}
                basicSetup={{
                  lineNumbers: true,
                  foldGutter: false,
                  highlightActiveLine: true,
                  highlightActiveLineGutter: true,
                  autocompletion: true,
                  bracketMatching: true,
                  closeBrackets: true,
                  indentOnInput: true,
                }}
                placeholder="-- Write SQL here, e.g. SELECT * FROM `mongo`.`orders` LIMIT 10"
                className="h-full"
              />
            </div>
          </div>

          <div
            role="separator"
            aria-orientation="horizontal"
            aria-label="Resize results panel"
            onPointerDown={beginEditorResize}
            onDoubleClick={() => setEditorRatio(0.43)}
            className="group relative h-0 w-full shrink-0 cursor-row-resize touch-none"
          >
            <span className="absolute -top-1.5 left-0 h-3 w-full" />
            <span className="pointer-events-none absolute -top-px left-1/2 h-px w-12 -translate-x-1/2 bg-border transition-colors group-hover:bg-primary/60" />
          </div>

          <div className="flex min-h-0 flex-1 flex-col overflow-hidden surface">
            <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
              <div className="flex h-10 shrink-0 items-center justify-between border-b border-border/45 px-3">
                <TabsList className="h-7 bg-transparent p-0">
                  <TabsTrigger value="results" className="gap-1.5 px-2.5 data-[state=active]:bg-accent/70">
                    {t("Results")}
                    {lastRun && <Badge variant={lastRun.status === "success" ? "success" : "destructive"} className="px-1 py-0 text-[9px]">{lastRun.status === "success" ? formatNumber(lastRun.rowCount) : "!"}</Badge>}
                  </TabsTrigger>
                  <TabsTrigger value="history" className="gap-1.5 px-2.5 data-[state=active]:bg-accent/70">
                    {t("History")} <span className="text-[9px] text-muted-foreground">{runs.length}</span>
                  </TabsTrigger>
                </TabsList>
                {tab === "results" && result && (
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="xs" onClick={() => void navigator.clipboard.writeText(JSON.stringify(result.rows, null, 2)).then(() => toast.success("Results copied as JSON"))}>
                      <Copy className="size-3" /> <span className="hidden sm:inline">JSON</span>
                    </Button>
                    <Button variant="ghost" size="xs" onClick={() => downloadCSV(result.columns, result.rows, `${currentSaved?.name || "anyquery"}.csv`)}>
                      <Download className="size-3" /> <span className="hidden sm:inline">CSV</span>
                    </Button>
                  </div>
                )}
              </div>

              <TabsContent value="results" className="mt-0 min-h-0 flex-1 overflow-hidden">
                <ResultsTable result={result} running={running} error={error} errorCode={errorCode} errorHint={errorHint} />
              </TabsContent>
              <TabsContent value="history" className="mt-0 min-h-0 flex-1 overflow-y-auto scrollbar-thin">
                {runs.length === 0 ? (
                  <div className="grid h-full place-items-center p-6 text-center">
                    <div>
                      <History className="mx-auto size-6 text-muted-foreground/50" />
                      <p className="mt-2 text-sm text-muted-foreground">{t("No query history yet.")}</p>
                    </div>
                  </div>
                ) : (
                  <div>
                    <div className="divide-y divide-border/45">
                      {pageRuns.map((run) => (
                        <button key={run.id} onClick={() => loadRun(run)} className="group flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/40">
                          <div className={cn("mt-0.5 grid size-7 shrink-0 place-items-center rounded-md", run.status === "success" ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive")}>
                            {run.status === "success" ? <CheckCircle2 className="size-3.5" /> : <CircleAlert className="size-3.5" />}
                          </div>
                          <div className="min-w-0 flex-1">
                            <div className="line-clamp-1 font-mono text-[11px]">{truncate(run.sql.replace(/\s+/g, " "), 160)}</div>
                            <div className="mt-1 flex flex-wrap items-center gap-2 text-[10.5px] text-muted-foreground">
                              <span>{relativeTime(run.createdAt)}</span>
                              <span className="size-0.5 rounded-full bg-current" />
                              <span>{formatDuration(run.durationMs)}</span>
                              <span className="size-0.5 rounded-full bg-current" />
                              <span>{run.status === "success" ? `${formatNumber(run.rowCount)} rows` : run.error || "failed"}</span>
                            </div>
                          </div>
                          <span className="text-[10px] text-primary opacity-0 transition-opacity group-hover:opacity-100">Load</span>
                        </button>
                      ))}
                    </div>
                    {runs.length > historyPageSize && (
                      <div className="flex items-center justify-between border-t border-border/45 px-4 py-2 text-[10.5px] text-muted-foreground">
                        <span>{currentHistoryPage * historyPageSize + 1}–{Math.min((currentHistoryPage + 1) * historyPageSize, runs.length)} of {runs.length}</span>
                        <div className="flex items-center gap-1">
                          <button disabled={currentHistoryPage === 0} onClick={() => setHistoryPage((p) => Math.max(0, p - 1))} className="rounded px-1.5 py-0.5 hover:bg-accent disabled:opacity-30">Prev</button>
                          <button disabled={currentHistoryPage >= historyPageCount - 1} onClick={() => setHistoryPage((p) => Math.min(historyPageCount - 1, p + 1))} className="rounded px-1.5 py-0.5 hover:bg-accent disabled:opacity-30">Next</button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </TabsContent>
            </Tabs>
          </div>
        </div>
      </div>

      <Dialog open={paramsOpen} onOpenChange={setParamsOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("Bind parameters")}</DialogTitle>
            <DialogDescription>
              {t("Positional values for ? or $1 placeholders. Enter a JSON array.")}
            </DialogDescription>
          </DialogHeader>
          <textarea
            value={paramText}
            onChange={(e) => setParamText(e.target.value)}
            spellCheck={false}
            rows={7}
            placeholder={'[42, "paid", "2024-01-01T00:00:00Z"]'}
            className="w-full resize-y rounded-lg border bg-muted/20 px-3 py-2 font-mono text-[12px] outline-none focus:border-ring focus:ring-2 focus:ring-ring/20"
          />
          <DialogFooter>
            <Button variant="ghost" onClick={() => setParamText("")}>{t("Clear")}</Button>
            <Button onClick={() => setParamsOpen(false)}>{t("Done")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <SaveQueryDialog
        open={saveOpen}
        onOpenChange={setSaveOpen}
        sql={sqlText}
        editing={currentSaved}
        onSaved={(q) => {
          setCurrentSaved(q)
          setParams({ saved: q.id })
          void queryClient.invalidateQueries({ queryKey: ["saved-queries", tenant.slug] })
          void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
        }}
      />

      <Dialog open={schemaOpen} onOpenChange={setSchemaOpen}>
        <DialogContent className="p-0 sm:max-w-md">
          <DialogHeader className="sr-only"><DialogTitle>Schema explorer</DialogTitle></DialogHeader>
          <div className="h-[70vh]">{renderSchema(() => setSchemaOpen(false))}</div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
