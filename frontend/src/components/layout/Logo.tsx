import { cn } from "@/lib/utils"

export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 40" fill="none" className={cn("size-8", className)} aria-hidden>
      <defs>
        <linearGradient id="aq-logo" x1="4" y1="4" x2="36" y2="36" gradientUnits="userSpaceOnUse">
          <stop stopColor="#6366f1" />
          <stop offset="1" stopColor="#06b6d4" />
        </linearGradient>
      </defs>
      <rect width="40" height="40" rx="11" fill="url(#aq-logo)" />
      <ellipse cx="20" cy="13.5" rx="8.5" ry="3.4" stroke="white" strokeWidth="1.9" />
      <path d="M11.5 13.5v6.2c0 1.9 3.8 3.4 8.5 3.4s8.5-1.5 8.5-3.4v-6.2" stroke="white" strokeWidth="1.9" />
      <path d="M11.5 20.5v6.2c0 1.9 3.8 3.4 8.5 3.4s8.5-1.5 8.5-3.4v-6.2" stroke="white" strokeWidth="1.9" opacity=".62" />
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
