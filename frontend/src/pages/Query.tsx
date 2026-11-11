import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import CodeMirror, { type ReactCodeMirrorRef } from "@uiw/react-codemirror"
import { sql, StandardSQL } from "@codemirror/lang-sql"
import { keymap } from "@codemirror/view"
import { format as formatSQL } from "sql-formatter"
import {
  Bookmark,
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
  Save,
  Sparkles,
  SquareTerminal,
  Trash2,
  WandSparkles,
} from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useTenant } from "@/providers/tenant"
import { useTheme } from "@/providers/theme"
import { cn, downloadCSV, formatDuration, formatNumber, relativeTime, truncate } from "@/lib/utils"
import type { ExecuteResponse, QueryRun, QueryResult, SavedQuery } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { SchemaExplorer, type CatalogSource } from "@/components/query/SchemaExplorer"
import { ResultsTable } from "@/components/query/ResultsTable"
import { SaveQueryDialog } from "@/components/query/SaveQueryDialog"
import { ErrorNotice } from "@/components/shared"

export function QueryPage() {
  const { tenant, role } = useTenant()
  const { resolvedTheme } = useTheme()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [params, setParams] = useSearchParams()
  const savedId = params.get("saved") || ""
  const editorRef = useRef<ReactCodeMirrorRef>(null)
  const runRef = useRef<() => void>(() => {})
  const canEdit = role === "owner" || role === "admin" || role === "member"

  const { data: catalog, refetch: refetchCatalog, isLoading: catalogLoading } = useQuery({
    queryKey: ["catalog", tenant.slug],
    queryFn: () => api.catalog(tenant.slug),
  })
  const sources: CatalogSource[] = (catalog?.sources ?? []).map((s) => ({ ...s, tables: (s.tables ?? []).map((t) => ({ ...t, columns: t.columns ?? [] })) }))

  const { data: runsData } = useQuery({
    queryKey: ["runs", tenant.slug, "studio"],
    queryFn: () => api.runs(tenant.slug, 30),
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
  const [tab, setTab] = useState("results")

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

  const runQuery = useCallback(async () => {
    const text = sqlText.trim()
    if (!text || running) return
    setRunning(true)
    setError(null)
    setErrorCode(null)
    setErrorHint(null)
    setLastRun(null)
    setTab("results")
    try {
      const payload: ExecuteResponse = await api.execute(tenant.slug, text, currentSaved?.id)
      if (payload.error) {
        setError(payload.error.message)
        setErrorCode(payload.error.code)
        setErrorHint(payload.error.hint || null)
        setResult(null)
      } else if (payload.result) {
        setResult(payload.result)
      }
      if (payload.run) setLastRun(payload.run)
      void queryClient.invalidateQueries({ queryKey: ["runs", tenant.slug] })
      void queryClient.invalidateQueries({ queryKey: ["overview", tenant.slug] })
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

  function formatQuery() {
    if (!sqlText.trim()) return
    if (sqlText.includes("|>")) {
      toast.info("Pipeline queries are already readable — automatic formatting supports classic SQL.")
      return
    }
    try {
      setSQLText(formatSQL(sqlText, { language: "sql", keywordCase: "upper", tabWidth: 2 }))
    } catch {
      toast.error("Could not format this query")
    }
  }

  const sourceCount = sources.length
  const tableCount = sources.reduce((n, s) => n + s.tables.length, 0)
  const runs = runsData?.runs ?? []

  const schemaPanel = (
    <SchemaExplorer
      sources={sources}
      loading={catalogLoading}
      onInsert={insertText}
      onRefresh={() => void refetchCatalog()}
      onAddSource={() => navigate(`/w/${tenant.slug}/sources?new=1`)}
      onManage={() => navigate(`/w/${tenant.slug}/sources`)}
    />
  )

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-[52px] shrink-0 items-center gap-2 border-b bg-background px-3 md:px-4">
        <div className="flex min-w-0 items-center gap-2">
          <div className="hidden size-7 place-items-center rounded-md bg-primary/10 text-primary sm:grid">
            <SquareTerminal className="size-3.5" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-[13px] font-semibold">{currentSaved?.name || "Untitled query"}</span>
              {currentSaved && <Badge variant="outline" className="hidden text-[9.5px] capitalize sm:inline-flex">{currentSaved.visibility}</Badge>}
              {currentSaved && <CheckCircle2 className="hidden size-3 text-success sm:block" />}
            </div>
            <div className="hidden text-[10.5px] text-muted-foreground sm:block">
              {sourceCount} sources · {tableCount} tables · {tenant.name}
            </div>
          </div>
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="sm" className="hidden md:inline-flex" onClick={newQuery}><FileCode2 className="size-3.5" /> New</Button>
          <Button variant="outline" size="sm" className="hidden sm:inline-flex" onClick={formatQuery} title="Format SQL"><WandSparkles className="size-3.5" /> Format</Button>
          <Button variant="outline" size="sm" className="lg:hidden" onClick={() => setSchemaOpen(true)}><PanelLeft className="size-3.5" /> Schema</Button>
          {canEdit && (
            <Button variant="outline" size="sm" onClick={() => setSaveOpen(true)} disabled={!sqlText.trim()}>
              <Save className="size-3.5" /> <span className="hidden sm:inline">{currentSaved ? "Update" : "Save"}</span>
            </Button>
          )}
          <Button size="sm" onClick={() => void runQuery()} disabled={running || !sqlText.trim()} className="min-w-[82px]">
            {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5 fill-current" />}
            {running ? "Running" : "Run"}
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-[268px] shrink-0 border-r lg:block">{schemaPanel}</aside>

        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex h-[43%] min-h-[190px] flex-col border-b bg-card">
            <div className="flex h-9 shrink-0 items-center justify-between border-b px-3 text-[11px] text-muted-foreground">
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center gap-1 font-mono text-[10px]"><span className="size-1.5 rounded-full bg-emerald-500" /> SQL</span>
                {currentSaved && <span className="hidden items-center gap-1 sm:inline-flex"><Bookmark className="size-3" /> saved</span>}
              </div>
              <div className="flex items-center gap-2 text-[10px]">
                <span className="hidden items-center gap-1 sm:inline-flex"><Sparkles className="size-3" /> Cmd/Ctrl + Enter to run</span>
                <button className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-accent" onClick={() => setTab("history")}><History className="size-3" /> History</button>
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

          <div className="flex min-h-0 flex-1 flex-col bg-background">
            <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
              <div className="flex h-10 shrink-0 items-center justify-between border-b px-3">
                <TabsList className="h-7 bg-transparent p-0">
                  <TabsTrigger value="results" className="gap-1.5 px-2.5 data-[state=active]:bg-accent/70">
                    Results
                    {lastRun && <Badge variant={lastRun.status === "success" ? "success" : "destructive"} className="px-1 py-0 text-[9px]">{lastRun.status === "success" ? formatNumber(lastRun.rowCount) : "!"}</Badge>}
                  </TabsTrigger>
                  <TabsTrigger value="history" className="gap-1.5 px-2.5 data-[state=active]:bg-accent/70">
                    History <span className="text-[9px] text-muted-foreground">{runs.length}</span>
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
                      <p className="mt-2 text-sm text-muted-foreground">No query history yet.</p>
                    </div>
                  </div>
                ) : (
                  <div className="divide-y">
                    {runs.map((run) => (
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
                )}
              </TabsContent>
            </Tabs>
          </div>
        </div>
      </div>

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
          <div className="h-[70vh]">{schemaPanel}</div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
