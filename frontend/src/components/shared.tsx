import { Activity, Braces, Cloud, Copy, Database, FileSpreadsheet, Globe2, Layers, Loader2, Network, Search, Snowflake, Boxes, Zap, type LucideIcon } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import type { Role } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"

const typeMeta: Record<string, { icon: LucideIcon; color: string; bg: string; label: string }> = {
  mongodb: { icon: Database, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "MongoDB" },
  redis: { icon: Layers, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Redis" },
  postgres: { icon: Database, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "PostgreSQL" },
  cockroachdb: { icon: Database, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "CockroachDB" },
  redshift: { icon: Database, color: "text-rose-600 dark:text-rose-400", bg: "bg-rose-500/10", label: "Redshift" },
  timescaledb: { icon: Activity, color: "text-amber-600 dark:text-amber-400", bg: "bg-amber-500/10", label: "TimescaleDB" },
  influxdb: { icon: Activity, color: "text-pink-600 dark:text-pink-400", bg: "bg-pink-500/10", label: "InfluxDB" },
  questdb: { icon: Activity, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "QuestDB" },
  greptimedb: { icon: Activity, color: "text-lime-600 dark:text-lime-400", bg: "bg-lime-500/10", label: "GreptimeDB" },
  sls: { icon: Cloud, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "Alibaba Cloud SLS" },
  tdengine: { icon: Activity, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "TDengine" },
  druid: { icon: Activity, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Apache Druid" },
  excel: { icon: FileSpreadsheet, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "Microsoft Excel" },
  access: { icon: Database, color: "text-blue-700 dark:text-blue-400", bg: "bg-blue-500/10", label: "Microsoft Access" },
  yugabytedb: { icon: Database, color: "text-lime-600 dark:text-lime-400", bg: "bg-lime-500/10", label: "YugabyteDB" },
  mysql: { icon: Database, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "MySQL" },
  mariadb: { icon: Database, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "MariaDB" },
  singlestore: { icon: Database, color: "text-fuchsia-600 dark:text-fuchsia-400", bg: "bg-fuchsia-500/10", label: "SingleStore" },
  sqlserver: { icon: Database, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "SQL Server" },
  sqlite: { icon: Database, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "SQLite" },
  clickhouse: { icon: Zap, color: "text-yellow-500", bg: "bg-yellow-500/10", label: "ClickHouse" },
  trino: { icon: Network, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Trino" },
  presto: { icon: Network, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "Presto" },
  snowflake: { icon: Snowflake, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10", label: "Snowflake" },
  databricks: { icon: Boxes, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Databricks" },
  oracle: { icon: Database, color: "text-red-700 dark:text-red-400", bg: "bg-red-500/10", label: "Oracle" },
  elasticsearch: { icon: Search, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "Elasticsearch" },
  opensearch: { icon: Search, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "OpenSearch" },
  rest: { icon: Globe2, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "REST / JSON" },
  csv: { icon: FileSpreadsheet, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "CSV" },
  jsonl: { icon: Braces, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "JSON Lines" },
}

export function sourceMeta(type: string) {
  return typeMeta[type] ?? { icon: Database, color: "text-muted-foreground", bg: "bg-muted", label: type }
}

export function SourceIcon({ type, className, size = "md" }: { type: string; className?: string; size?: "sm" | "md" | "lg" }) {
  const meta = sourceMeta(type)
  const Icon = meta.icon
  return (
    <div
      className={cn(
        "grid shrink-0 place-items-center rounded-lg border border-transparent",
        meta.bg,
        size === "sm" && "size-7 rounded-md",
        size === "md" && "size-9",
        size === "lg" && "size-11 rounded-xl",
        className,
      )}
    >
      <Icon className={cn(meta.color, size === "sm" ? "size-3.5" : size === "md" ? "size-4" : "size-5")} />
    </div>
  )
}

export function PageHeader({
  title,
  description,
  actions,
  eyebrow,
  className,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  eyebrow?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between", className)}>
      <div className="min-w-0">
        {eyebrow && <div className="mb-2 text-[11px] font-medium tracking-[0.14em] text-muted-foreground uppercase">{eyebrow}</div>}
        <h1 className="text-xl font-semibold tracking-tight md:text-2xl">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-[13px] leading-relaxed text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Page({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("h-full overflow-y-auto scrollbar-thin", className)}>{children}</div>
}

export function PageBody({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("mx-auto w-full max-w-[1320px] px-5 py-6 md:px-8 md:py-8", className)}>{children}</div>
}

export function RoleBadge({ role }: { role: Role }) {
  const variant = role === "owner" ? "default" : role === "admin" ? "secondary" : role === "viewer" ? "outline" : "muted"
  return <Badge variant={variant} className="capitalize">{role}</Badge>
}

export function StatusBadge({ status }: { status: string }) {
  if (status === "ready" || status === "success") return <Badge variant="success">Healthy</Badge>
  if (status === "error") return <Badge variant="destructive">Error</Badge>
  return <Badge variant="muted">{status}</Badge>
}

export function StatCard({
  label,
  value,
  icon: Icon,
  detail,
  className,
}: {
  label: string
  value: React.ReactNode
  icon: LucideIcon
  detail?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("group relative overflow-hidden surface p-5", className)}>
      <div className="flex items-start justify-between">
        <div>
          <div className="text-[12px] font-medium text-muted-foreground">{label}</div>
          <div className="mt-2 text-2xl font-semibold tracking-tight">{value}</div>
        </div>
        <div className="grid size-9 place-items-center rounded-lg bg-primary/8 text-primary">
          <Icon className="size-4" />
        </div>
      </div>
      {detail && <div className="mt-3 text-xs text-muted-foreground">{detail}</div>}
      <div className="pointer-events-none absolute -right-8 -bottom-8 size-24 rounded-full bg-primary/5 blur-2xl transition-opacity group-hover:opacity-100" />
    </div>
  )
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: {
  icon: LucideIcon
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center surface-quiet px-6 py-14 text-center", className)}>
      <div className="mb-4 grid size-11 place-items-center rounded-xl border bg-background text-muted-foreground shadow-sm">
        <Icon className="size-5" />
      </div>
      <div className="text-sm font-semibold">{title}</div>
      {description && <div className="mt-1 max-w-sm text-[13px] leading-relaxed text-muted-foreground">{description}</div>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}

export function LoadingBlock({ label = "Loading…", className }: { label?: string; className?: string }) {
  return (
    <div className={cn("flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground", className)}>
      <Loader2 className="size-4 animate-spin" />
      {label}
    </div>
  )
}

export function FullPageLoader({ label = "Loading AnyQuery…" }: { label?: string }) {
  return (
    <div className="grid min-h-screen place-items-center bg-background">
      <div className="flex flex-col items-center gap-4">
        <div className="relative">
          <div className="absolute inset-0 rounded-2xl bg-primary/20 blur-xl" />
          <div className="relative grid size-12 place-items-center rounded-2xl bg-primary text-primary-foreground">
            <Database className="size-5" />
          </div>
        </div>
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" /> {label}
        </div>
      </div>
    </div>
  )
}

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      className={cn("text-muted-foreground", className)}
      onClick={() => {
        void navigator.clipboard.writeText(value).then(() => toast.success("Copied to clipboard"))
      }}
    >
      <Copy className="size-3.5" /> {label}
    </Button>
  )
}

export function InlineCode({ children, className }: { children: React.ReactNode; className?: string }) {
  return <code className={cn("rounded-md border bg-muted/60 px-1.5 py-0.5 font-mono text-[11.5px]", className)}>{children}</code>
}

export function ErrorNotice({ message, hint }: { message?: string | null; hint?: string | null }) {
  if (!message) return null
  return (
    <div className="rounded-lg border border-destructive/25 bg-destructive/5 px-3.5 py-3 text-[13px] text-destructive">
      <div className="font-medium">{message}</div>
      {hint && <div className="mt-1 text-xs text-destructive/75">{hint}</div>}
    </div>
  )
}
