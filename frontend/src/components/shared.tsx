import { Copy, Database, Loader2, type LucideIcon } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import type { Role } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"

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
  const { t } = useI18n()
  if (status === "ready" || status === "success") return <Badge variant="success">{t("Healthy")}</Badge>
  if (status === "error") return <Badge variant="destructive">{t("Error")}</Badge>
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

export function LoadingBlock({ label, className }: { label?: string; className?: string }) {
  const { t } = useI18n()
  return (
    <div className={cn("flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground", className)}>
      <Loader2 className="size-4 animate-spin" />
      {label ?? t("Loading…")}
    </div>
  )
}

export function FullPageLoader({ label }: { label?: string }) {
  const { t } = useI18n()
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
          <Loader2 className="size-3.5 animate-spin" /> {label ?? t("Loading AnyQuery…")}
        </div>
      </div>
    </div>
  )
}

export function CopyButton({ value, label, className }: { value: string; label?: string; className?: string }) {
  const { t } = useI18n()
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      className={cn("text-muted-foreground", className)}
      onClick={() => {
        void navigator.clipboard.writeText(value).then(() => toast.success(t("Copied to clipboard")))
      }}
    >
      <Copy className="size-3.5" /> {label ?? t("Copy")}
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
