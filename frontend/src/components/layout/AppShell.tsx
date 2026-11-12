import { useState } from "react"
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom"
import {
  Activity,
  Bookmark,
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
} from "lucide-react"
import { cn, initials } from "@/lib/utils"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { useTenant } from "@/providers/tenant"
import { useTheme } from "@/providers/theme"
import { Logo, LogoMark } from "@/components/layout/Logo"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
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
  const { me, logout } = useAuth()
  const { tenant, role } = useTenant()
  const { theme, setTheme, resolvedTheme } = useTheme()
  const navigate = useNavigate()
  const location = useLocation()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)

  const otherTenants = me?.tenants.filter((t) => t.id !== tenant.id) ?? []

  async function signOut() {
    await logout()
    navigate("/login", { replace: true })
  }

  const nav = (
    <div className="flex h-full flex-col">
      <div className="flex h-16 items-center justify-between px-5">
        <NavLink to={`/w/${tenant.slug}`} onClick={() => setMobileOpen(false)}>
          <Logo />
        </NavLink>
        <Button variant="ghost" size="icon-sm" className="md:hidden" onClick={() => setMobileOpen(false)}>
          <X className="size-4" />
        </Button>
      </div>

      <div className="px-3">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
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
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-[232px]">
            <DropdownMenuLabel className="text-[11px]">Workspaces</DropdownMenuLabel>
            {me?.tenants.map((t) => (
              <TenantMenuItem key={t.id} tenant={t} current={t.id === tenant.id} onSelect={() => navigate(`/w/${t.slug}`)} />
            ))}
            {otherTenants.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">You’re in your only workspace</div>}
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => setCreateOpen(true)}>
              <Plus className="size-4" /> New workspace
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <nav className="mt-5 flex-1 space-y-6 overflow-y-auto px-3 scrollbar-thin">
        {navGroups.map((group) => (
          <div key={group.label}>
            <div className="mb-1.5 px-2.5 text-[10px] font-medium tracking-[0.13em] text-muted-foreground/80 uppercase">{group.label}</div>
            <div className="space-y-0.5">
              {group.items.map((item) => {
                const href = item.to ? `/w/${tenant.slug}/${item.to}` : `/w/${tenant.slug}`
                return (
                  <NavLink
                    key={item.to || "overview"}
                    to={href}
                    end={item.end}
                    onClick={() => setMobileOpen(false)}
                    className={({ isActive }) =>
                      cn(
                        "group relative flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-[13px] font-medium transition-colors",
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
                        {item.label}
                      </>
                    )}
                  </NavLink>
                )
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className="p-4">
        <div className="mb-2 flex items-center justify-between px-1">
          <span className="text-[11px] text-muted-foreground">Appearance</span>
          <div className="flex items-center rounded-md border bg-background p-0.5">
            <ThemeButton value="light" current={theme} onSelect={setTheme} label="Light"><Sun className="size-3" /></ThemeButton>
            <ThemeButton value="dark" current={theme} onSelect={setTheme} label="Dark"><Moon className="size-3" /></ThemeButton>
            <ThemeButton value="system" current={theme} onSelect={setTheme} label="System"><Monitor className="size-3" /></ThemeButton>
          </div>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
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
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="start" className="w-[224px]">
            <DropdownMenuLabel>
              <div className="flex items-center justify-between">
                <span className="truncate">{me?.user.name}</span>
                <RoleBadge role={role} />
              </div>
              <div className="mt-0.5 truncate text-[11px] font-normal text-muted-foreground">{me?.user.email}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => navigate(`/w/${tenant.slug}/settings`)}>
              <Settings className="size-4" /> Workspace settings
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}>
              {resolvedTheme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
              {resolvedTheme === "dark" ? "Light theme" : "Dark theme"}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => void signOut()}>
              <LogOut className="size-4" /> Sign out
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
      <aside className="hidden w-[252px] shrink-0 bg-sidebar md:block">{nav}</aside>

      {mobileOpen && (
        <div className="fixed inset-0 z-50 md:hidden">
          <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" onClick={() => setMobileOpen(false)} />
          <aside className="absolute inset-y-0 left-0 w-[272px] bg-sidebar shadow-2xl">{nav}</aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between bg-background/90 px-4 backdrop-blur md:hidden">
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="icon-sm" onClick={() => setMobileOpen(true)}>
              <Menu className="size-4" />
            </Button>
            <LogoMark className="size-6" />
            <span className="text-sm font-semibold">{title || tenant.name}</span>
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

function ThemeButton({
  value,
  current,
  onSelect,
  label,
  children,
}: {
  value: "light" | "dark" | "system"
  current: string
  onSelect: (value: "light" | "dark" | "system") => void
  label: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={() => onSelect(value)}
      className={cn(
        "grid size-6 place-items-center rounded text-muted-foreground transition-colors",
        current === value && "bg-accent text-accent-foreground shadow-xs",
      )}
    >
      {children}
    </button>
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
            <DialogTitle>Create a workspace</DialogTitle>
            <DialogDescription>
              Workspaces keep data sources, queries and members isolated. You’ll be the owner.
            </DialogDescription>
          </DialogHeader>
          <div className="mt-5 space-y-2">
            <Label htmlFor="workspace-name">Workspace name</Label>
            <Input
              id="workspace-name"
              autoFocus
              placeholder="e.g. Revenue Analytics"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">A URL-friendly slug is generated automatically.</p>
          </div>
          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? "Creating…" : "Create workspace"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
