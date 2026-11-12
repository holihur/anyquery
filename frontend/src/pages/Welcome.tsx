import { useState } from "react"
import { Navigate, useNavigate } from "react-router-dom"
import { ArrowRight, Database, LogOut, Users } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Logo } from "@/components/layout/Logo"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ErrorNotice } from "@/components/shared"
import { toast } from "sonner"

export function WelcomePage() {
  const { me, refresh, logout } = useAuth()
  const navigate = useNavigate()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (me?.tenants.length) {
    return <Navigate to={`/w/${me.tenants[0].slug}`} replace />
  }

  async function create(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    setError(null)
    try {
      const t = await api.createTenant(name.trim())
      await refresh()
      toast.success(`Welcome to ${t.name}`)
      navigate(`/w/${t.slug}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create workspace")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen flex-col bg-background">
      <div className="absolute inset-0 dot-bg opacity-40" />
      <header className="relative flex items-center justify-between px-6 py-5 md:px-10">
        <Logo />
        <div className="flex items-center gap-3 text-sm">
          <span className="hidden text-muted-foreground sm:inline">{me?.user.email}</span>
          <Button variant="ghost" size="sm" onClick={() => void logout().then(() => navigate("/login"))}>
            <LogOut className="size-4" /> Sign out
          </Button>
        </div>
      </header>
      <div className="relative flex flex-1 items-center justify-center px-6 pb-20">
        <div className="w-full max-w-[440px]">
          <div className="mb-7 text-center">
            <div className="mx-auto mb-5 grid size-12 place-items-center rounded-2xl border bg-card shadow-sm">
              <Database className="size-5 text-primary" />
            </div>
            <h1 className="text-2xl font-semibold tracking-tight">Create your first workspace</h1>
            <p className="mt-2 text-[13.5px] leading-relaxed text-muted-foreground">
              A workspace is where your team connects data sources, saves queries and collaborates.
            </p>
          </div>
          <Card>
            <CardHeader>
              <CardTitle>Workspace details</CardTitle>
              <CardDescription>You can invite teammates after creating it.</CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={create} className="space-y-4">
                <div className="space-y-1.5">
                  <Label htmlFor="name">Name</Label>
                  <Input id="name" autoFocus placeholder="Acme Analytics" value={name} onChange={(e) => setName(e.target.value)} />
                </div>
                {error && <ErrorNotice message={error} />}
                <Button type="submit" className="w-full" disabled={busy || !name.trim()}>
                  {busy ? "Creating…" : "Create workspace"} <ArrowRight className="size-4" />
                </Button>
              </form>
              <div className="mt-6 grid grid-cols-2 gap-3 border-t border-border/50 pt-5">
                <div className="flex items-start gap-2 text-[11.5px] leading-relaxed text-muted-foreground">
                  <Users className="mt-0.5 size-3.5 shrink-0" /> Invite teammates with roles
                </div>
                <div className="flex items-start gap-2 text-[11.5px] leading-relaxed text-muted-foreground">
                  <Database className="mt-0.5 size-3.5 shrink-0" /> Connect 20+ data sources
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  )
}
