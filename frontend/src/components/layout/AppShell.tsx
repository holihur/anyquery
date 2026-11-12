import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom"
import {
  Activity,
  Globe2,
  Bookmark,
  ChevronDown,
  ChevronsUpDown,
  Database,
  LayoutDashboard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Plus,
  Settings,
  SquareTerminal,
  Sun,
  Users,
  X,
  Check,
  PanelLeftClose,
  PanelLeftOpen,
} from "lucide-react"
import { cn, initials } from "@/lib/utils"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { useTenant } from "@/providers/tenant"
import { useTheme } from "@/providers/theme"
import { useI18n } from "@/i18n"
import { LanguageRadioItems } from "@/components/layout/LanguageMenu"
import { Logo, LogoMark } from "@/components/layout/Logo"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { RoleBadge } from "@/components/shared"
import { toast } from "sonner"
import type { TenantRole } from "@/lib/types"

const navGroups = [
  {
    label: "Workspace",
    items: [
      { to: "", label: "Overview", icon: LayoutDashboard, end: true },
      { to: "query", label: "Query studio", icon: SquareTerminal },
      { to: "sources", label: "Data sources", icon: Database },
      { to: "saved", label: "Saved queries", icon: Bookmark },
    ],
  },
  {
    label: "Collaborate",
    items: [
      { to: "team", label: "Team", icon: Users },
      { to: "activity", label: "Activity", icon: Activity },
    ],
  },
  {
    label: "Manage",
    items: [{ to: "settings", label: "Settings", icon: Settings }],
  },
]

export function AppShell() {
  const { t, locale, setLocale } = useI18n()
  const { me, logout } = useAuth()
  const { tenant, role } = useTenant()
  const { theme, setTheme } = useTheme()
  const navigate = useNavigate()
  const location = useLocation()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("aq-sidebar-collapsed") === "1")
  const [savedExpanded, setSavedExpanded] = useState(() => localStorage.getItem("aq-saved-nav-expanded") === "1")
  const { data: savedQueries } = useQuery({
    queryKey: ["saved-queries", tenant.slug],
    queryFn: () => api.savedQueries(tenant.slug),
  })

  useEffect(() => {
    localStorage.setItem("aq-saved-nav-expanded", savedExpanded ? "1" : "0")
  }, [savedExpanded])

  useEffect(() => {
    if (location.pathname.endsWith("/saved") || new URLSearchParams(location.search).has("saved")) {
      setSavedExpanded(true)
    }
  }, [location.pathname, location.search])

  useEffect(() => {
    localStorage.setItem("aq-sidebar-collapsed", collapsed ? "1" : "0")
  }, [collapsed])

  const otherTenants = me?.tenants.filter((t) => t.id !== tenant.id) ?? []

  async function signOut() {
    await logout()
    navigate("/login", { replace: true })
  }

  const renderNav = (compact: boolean) => (
    <div className="flex h-full flex-col">
      <div className={cn("flex h-16 items-center", compact ? "justify-center px-2" : "justify-between px-5")}>
        <NavLink to={`/w/${tenant.slug}`} onClick={() => setMobileOpen(false)} className="flex items-center">
          {compact ? <LogoMark /> : <Logo />}
        </NavLink>
        {!compact && (
          <div className="flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" className="hidden md:inline-flex" onClick={() => setCollapsed(true)} aria-label={t("Collapse sidebar")}>
                  <PanelLeftClose className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">{t("Collapse sidebar")}</TooltipContent>
            </Tooltip>
            <Button variant="ghost" size="icon-sm" className="md:hidden" onClick={() => setMobileOpen(false)} aria-label={t("Close navigation")}>
              <X className="size-4" />
            </Button>
          </div>
        )}
      </div>

      {compact && (
        <div className="flex justify-center pb-1">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-xs" onClick={() => setCollapsed(false)} aria-label={t("Expand sidebar")}>
                <PanelLeftOpen className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="right">{t("Expand sidebar")}</TooltipContent>
          </Tooltip>
        </div>
      )}

      <div className={compact ? "px-2" : "px-3"}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            {compact ? (
              <button
                className="mx-auto grid size-9 place-items-center rounded-md bg-primary text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90"
                title={`${tenant.name} · ${role}`}
              >
                {tenant.name.slice(0, 1).toUpperCase()}
              </button>
            ) : (
              <button className="group flex w-full items-center gap-2.5 rounded-lg bg-background/80 px-2.5 py-2 text-left shadow-xs transition-colors hover:bg-background">
                <div className="grid size-8 shrink-0 place-items-center rounded-md bg-primary text-[13px] font-semibold text-primary-foreground">
                  {tenant.name.slice(0, 1).toUpperCase()}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-medium">{tenant.name}</div>
                  <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <span className="capitalize">{role}</span>
                    <span className="size-0.5 rounded-full bg-current" />
                    <span className="truncate">{tenant.slug}</span>
                  </div>
                </div>
                <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
              </button>
            )}
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" side={compact ? "right" : "bottom"} className="w-[232px]">
            <DropdownMenuLabel className="text-[11px]">{t("Workspaces")}</DropdownMenuLabel>
            {me?.tenants.map((t) => (
              <TenantMenuItem key={t.id} tenant={t} current={t.id === tenant.id} onSelect={() => navigate(`/w/${t.slug}`)} />
            ))}
            {otherTenants.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">{t("You’re in your only workspace")}</div>}
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => setCreateOpen(true)}>
              <Plus className="size-4" /> {t("New workspace")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <nav className={cn("mt-5 flex-1 overflow-y-auto scrollbar-thin", compact ? "space-y-3 px-2" : "space-y-6 px-3")}>
        {navGroups.map((group) => (
          <div key={group.label}>
            {!compact && (
              <div className="mb-1.5 px-2.5 text-[10px] font-medium tracking-[0.13em] text-muted-foreground/80 uppercase">{t(group.label)}</div>
            )}
            <div className="space-y-0.5">
              {group.items.map((item) => {
                const href = item.to ? `/w/${tenant.slug}/${item.to}` : `/w/${tenant.slug}`
                if (item.to === "saved" && !compact) {
                  const queries = savedQueries?.queries ?? []
                  const preview = queries.slice(0, 5)
                  return (
                    <div key="saved">
                      <div className="group relative flex items-center">
                        <NavLink
                          to={href}
                          onClick={() => setMobileOpen(false)}
                          className={({ isActive }) =>
                            cn(
                              "group relative flex h-9 flex-1 items-center gap-2.5 rounded-lg px-2.5 pr-8 text-[13px] font-medium transition-colors",
                              isActive
                                ? "bg-sidebar-accent text-sidebar-accent-foreground shadow-xs"
                                : "text-sidebar-foreground/75 hover:bg-sidebar-accent/55 hover:text-sidebar-accent-foreground",
                            )
                          }
                        >
                          {({ isActive }) => (
                            <>
                              <span className={cn("absolute left-0 h-4 w-0.5 rounded-full bg-primary transition-opacity", isActive ? "opacity-100" : "opacity-0")} />
                              <item.icon className="size-4 shrink-0 opacity-80" />
                              <span className="truncate">{t(item.label)}</span>
                            </>
                          )}
                        </NavLink>
                        <button
                          type="button"
                          aria-label={savedExpanded ? "Collapse saved queries" : "Expand saved queries"}
                          onClick={() => setSavedExpanded((value) => !value)}
                          className="absolute right-1 grid size-6 place-items-center rounded text-muted-foreground/65 transition-colors hover:bg-sidebar-accent hover:text-foreground"
                        >
                          <ChevronDown className={cn("size-3.5 transition-transform", savedExpanded && "rotate-180")} />
                        </button>
                      </div>
                      {savedExpanded && (
                        <div className="mt-1 ml-4 space-y-0.5 border-l border-border/50 pl-2">
                          {preview.map((saved) => {
                            const active = new URLSearchParams(location.search).get("saved") === saved.id
                            return (
                              <Link
                                key={saved.id}
                                to={`/w/${tenant.slug}/query?saved=${saved.id}`}
                                onClick={() => setMobileOpen(false)}
                                className={cn(
                                  "flex items-center gap-1.5 rounded-md px-2 py-1.5 text-[11.5px] transition-colors",
                                  active
                                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                                    : "text-sidebar-foreground/60 hover:bg-sidebar-accent/55 hover:text-sidebar-accent-foreground",
                                )}
                              >
                                <Bookmark className="size-3 shrink-0 opacity-55" />
                                <span className="truncate">{saved.name}</span>
                              </Link>
                            )
                          })}
                          {queries.length === 0 && <div className="px-2 py-1 text-[10.5px] text-muted-foreground/70">{t("No saved queries")}</div>}
                          <Link
                            to={`/w/${tenant.slug}/saved`}
                            onClick={() => setMobileOpen(false)}
                            className="block rounded-md px-2 py-1.5 text-[10.5px] font-medium text-primary/80 hover:bg-sidebar-accent/55 hover:text-primary"
                          >
                            {queries.length > 5 ? t("View all {count}", { count: queries.length }) : t("Open library")}
                          </Link>
                        </div>
                      )}
                    </div>
                  )
                }
                return (
                  <NavLink
                    key={item.to || "overview"}
                    to={href}
                    end={item.end}
                    onClick={() => setMobileOpen(false)}
                    title={compact ? t(item.label) : undefined}
                    aria-label={compact ? t(item.label) : undefined}
                    className={({ isActive }) =>
                      cn(
                        "group relative flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-[13px] font-medium transition-colors",
                        compact && "justify-center px-0",
                        isActive
                          ? "bg-sidebar-accent text-sidebar-accent-foreground shadow-xs"
                          : "text-sidebar-foreground/75 hover:bg-sidebar-accent/55 hover:text-sidebar-accent-foreground",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <span className={cn("absolute left-0 h-4 w-0.5 rounded-full bg-primary transition-opacity", isActive ? "opacity-100" : "opacity-0")} />
                        <item.icon className="size-4 shrink-0 opacity-80" />
                        <span className={cn("truncate", compact && "sr-only")}>{t(item.label)}</span>
                      </>
                    )}
                  </NavLink>
                )
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className={compact ? "p-2" : "p-4"}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            {compact ? (
              <button className="mx-auto grid size-9 place-items-center rounded-lg transition-colors hover:bg-sidebar-accent/60" title={`${me?.user.name} · ${me?.user.email}`}>
                <Avatar className="size-7">
                  {me?.user.avatarUrl && <AvatarImage src={me.user.avatarUrl} />}
                  <AvatarFallback>{initials(me?.user.name, me?.user.email)}</AvatarFallback>
                </Avatar>
              </button>
            ) : (
              <button className="flex w-full items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-colors hover:bg-sidebar-accent/60">
                <Avatar className="size-8">
                  {me?.user.avatarUrl && <AvatarImage src={me.user.avatarUrl} />}
                  <AvatarFallback>{initials(me?.user.name, me?.user.email)}</AvatarFallback>
                </Avatar>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-medium">{me?.user.name}</div>
                  <div className="truncate text-[11px] text-muted-foreground">{me?.user.email}</div>
                </div>
                <ChevronsUpDown className="size-3.5 text-muted-foreground" />
              </button>
            )}
          </DropdownMenuTrigger>
          <DropdownMenuContent side={compact ? "right" : "top"} align={compact ? "end" : "start"} className="w-[224px]">
            <DropdownMenuLabel>
              <div className="flex items-center justify-between">
                <span className="truncate">{me?.user.name}</span>
                <RoleBadge role={role} />
              </div>
              <div className="mt-0.5 truncate text-[11px] font-normal text-muted-foreground">{me?.user.email}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => navigate(`/w/${tenant.slug}/settings`)}>
              <Settings className="size-4" /> {t("Workspace settings")}
            </DropdownMenuItem>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <Sun className="size-4" /> {t("Theme")}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as "light" | "dark" | "system")}>
                  <DropdownMenuRadioItem value="light"><Sun className="size-4" /> {t("Light")}</DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="dark"><Moon className="size-4" /> {t("Dark")}</DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="system"><Monitor className="size-4" /> {t("System")}</DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <Globe2 className="size-4" /> {t("Language")}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="max-h-[360px] w-[228px] overflow-y-auto">
                <LanguageRadioItems value={locale} onChange={setLocale} />
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => void signOut()}>
              <LogOut className="size-4" /> {t("Sign out")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  )


  const title = navGroups.flatMap((g) => g.items).find((item) => {
    const href = item.to ? `/w/${tenant.slug}/${item.to}` : `/w/${tenant.slug}`
    return item.end ? location.pathname === href : location.pathname.startsWith(href)
  })?.label

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <aside className={cn("sidebar-shell hidden shrink-0 bg-sidebar transition-[width] duration-200 ease-out md:block", collapsed ? "w-[76px]" : "w-[252px]")}>{renderNav(collapsed)}</aside>

      {mobileOpen && (
        <div className="fixed inset-0 z-50 md:hidden">
          <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" onClick={() => setMobileOpen(false)} />
          <aside className="absolute inset-y-0 left-0 w-[272px] bg-sidebar shadow-2xl">{renderNav(false)}</aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between bg-background/90 px-4 backdrop-blur md:hidden">
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="icon-sm" onClick={() => setMobileOpen(true)}>
              <Menu className="size-4" />
            </Button>
            <LogoMark className="size-6" />
            <span className="text-sm font-semibold">{title ? t(title) : tenant.name}</span>
          </div>
          <button className="flex items-center gap-2" onClick={() => setMobileOpen(true)}>
            <Avatar className="size-7">
              {me?.user.avatarUrl && <AvatarImage src={me.user.avatarUrl} />}
              <AvatarFallback>{initials(me?.user.name, me?.user.email)}</AvatarFallback>
            </Avatar>
          </button>
        </header>

        <main className="min-h-0 flex-1 overflow-hidden">
          <Outlet />
        </main>
      </div>

      <CreateWorkspaceDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  )
}

function TenantMenuItem({ tenant, current, onSelect }: { tenant: TenantRole; current: boolean; onSelect: () => void }) {
  return (
    <DropdownMenuItem onSelect={onSelect} className="gap-2.5">
      <div className="grid size-6 shrink-0 place-items-center rounded bg-primary/10 text-[10px] font-semibold text-primary">
        {tenant.name.slice(0, 1).toUpperCase()}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px]">{tenant.name}</div>
        <div className="truncate text-[10px] capitalize text-muted-foreground">{tenant.role}</div>
      </div>
      {current && <Check className="size-3.5 text-primary" />}
    </DropdownMenuItem>
  )
}


export function CreateWorkspaceDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: () => void
}) {
  const { t } = useI18n()
  const { refresh } = useAuth()
  const navigate = useNavigate()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    try {
      const tenant = await api.createTenant(name.trim())
      await refresh()
      toast.success(`Workspace “${tenant.name}” created`)
      onOpenChange(false)
      setName("")
      onCreated?.()
      navigate(`/w/${tenant.slug}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Could not create workspace")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("Create workspace")}</DialogTitle>
            <DialogDescription>
              {t("Workspaces keep data sources, queries and members isolated. You’ll be the owner.")}
            </DialogDescription>
          </DialogHeader>
          <div className="mt-5 space-y-2">
            <Label htmlFor="workspace-name">{t("Name")}</Label>
            <Input
              id="workspace-name"
              autoFocus
              placeholder="e.g. Revenue Analytics"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t("A URL-friendly slug is generated automatically.")}</p>
          </div>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("Cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("Creating…") : t("Create workspace")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
