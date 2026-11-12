import { cn } from "@/lib/utils"

export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 40" fill="none" className={cn("size-8 text-primary", className)} aria-hidden>
      <rect width="40" height="40" rx="8" fill="currentColor" />
      <ellipse cx="20" cy="13.5" rx="8.5" ry="3.4" stroke="var(--primary-foreground)" strokeWidth="1.9" />
      <path d="M11.5 13.5v6.2c0 1.9 3.8 3.4 8.5 3.4s8.5-1.5 8.5-3.4v-6.2" stroke="var(--primary-foreground)" strokeWidth="1.9" />
      <path d="M11.5 20.5v6.2c0 1.9 3.8 3.4 8.5 3.4s8.5-1.5 8.5-3.4v-6.2" stroke="var(--primary-foreground)" strokeWidth="1.9" opacity=".62" />
    </svg>
  )
}

export function Logo({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-2.5">
      <LogoMark />
      {!compact && (
        <div className="leading-none">
          <div className="text-[15px] font-semibold tracking-tight">AnyQuery</div>
          <div className="mt-1 text-[10px] font-medium tracking-[0.16em] text-muted-foreground uppercase">Federated SQL</div>
        </div>
      )}
    </div>
  )
}
