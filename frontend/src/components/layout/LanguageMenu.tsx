import { Globe2 } from "lucide-react"
import { locales, useI18n, type Locale } from "@/i18n"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export function LanguageRadioItems({ value, onChange }: { value: Locale; onChange: (locale: Locale) => void }) {
  return (
    <DropdownMenuRadioGroup value={value} onValueChange={(next) => onChange(next as Locale)}>
      {locales.map((option) => (
        <DropdownMenuRadioItem key={option.code} value={option.code} className="gap-2">
          <span className="min-w-0 flex-1">
            <span className="block text-[12.5px]">{option.label}</span>
            <span className="block text-[10px] text-muted-foreground">{option.english}</span>
          </span>
        </DropdownMenuRadioItem>
      ))}
    </DropdownMenuRadioGroup>
  )
}

export function LanguageMenu({ compact = false, align = "end" }: { compact?: boolean; align?: "start" | "center" | "end" }) {
  const { locale, setLocale, t } = useI18n()
  const current = locales.find((option) => option.code === locale) ?? locales[0]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size={compact ? "icon-sm" : "sm"} aria-label={t("Language")} title={t("Language")}>
          <Globe2 className="size-4" />
          {!compact && <span className="text-[12px]">{current.label}</span>}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="max-h-[360px] w-[228px] overflow-y-auto">
        <DropdownMenuLabel>{t("Language")}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <LanguageRadioItems value={locale} onChange={setLocale} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
