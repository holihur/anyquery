import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function initials(name?: string, email?: string) {
  const source = (name || email || "?").trim()
  const parts = source.split(/\s+/).filter(Boolean)
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase()
  return source.slice(0, 2).toUpperCase()
}

function activeLocale() {
  return localStorage.getItem("aq-locale") || document.documentElement.lang || navigator.language
}

export function formatNumber(n: number | null | undefined) {
  if (n === null || n === undefined) return "—"
  return new Intl.NumberFormat(activeLocale(), { maximumFractionDigits: 2 }).format(n)
}

export function formatDuration(ms?: number) {
  if (ms === undefined || ms === null) return "—"
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)} s`
}

export function relativeTime(value?: string | Date | null) {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  const diff = Date.now() - date.getTime()
  const abs = Math.abs(diff)
  const rtf = new Intl.RelativeTimeFormat(activeLocale(), { numeric: "auto" })
  if (abs < 45_000) return rtf.format(0, "second")
  if (abs < 3_600_000) return rtf.format(-Math.round(diff / 60_000), "minute")
  if (abs < 86_400_000) return rtf.format(-Math.round(diff / 3_600_000), "hour")
  if (abs < 604_800_000) return rtf.format(-Math.round(diff / 86_400_000), "day")
  return date.toLocaleDateString(activeLocale(), { month: "short", day: "numeric", year: "numeric" })
}

export function shortRelative(value?: string | Date | null) {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  const diff = Date.now() - date.getTime()
  const minutes = Math.round(diff / 60_000)
  const rtf = new Intl.RelativeTimeFormat(activeLocale(), { numeric: "auto", style: "narrow" })
  if (minutes < 1) return rtf.format(0, "second")
  if (minutes < 60) return rtf.format(-minutes, "minute")
  const hours = Math.round(minutes / 60)
  if (hours < 24) return rtf.format(-hours, "hour")
  const days = Math.round(hours / 24)
  if (days < 30) return rtf.format(-days, "day")
  return date.toLocaleDateString(activeLocale(), { month: "short", day: "numeric" })
}

export function formatDateTime(value?: string | Date | null) {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  return date.toLocaleString(activeLocale(), {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
}

export function truncate(s: string, n = 60) {
  return s.length > n ? `${s.slice(0, n)}…` : s
}

export function copyText(text: string) {
  return navigator.clipboard.writeText(text)
}

export function downloadCSV(columns: { name: string }[], rows: Record<string, unknown>[], filename = "anyquery-results.csv") {
  const escape = (v: unknown) => {
    if (v === null || v === undefined) return ""
    const s = typeof v === "object" ? JSON.stringify(v) : String(v)
    return `"${s.replaceAll('"', '""')}"`
  }
  const lines = [columns.map((c) => escape(c.name)).join(",")]
  for (const row of rows) {
    lines.push(columns.map((c) => escape(row[c.name])).join(","))
  }
  const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
