import { useEffect, useRef, useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { CheckCircle2, Loader2, MailWarning } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Logo } from "@/components/layout/Logo"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ErrorNotice } from "@/components/shared"
import { toast } from "sonner"

export function InvitePage() {
  const { token = "" } = useParams()
  const { me, loading, config, refresh, logout } = useAuth()
  const navigate = useNavigate()
  const [state, setState] = useState<"idle" | "accepting" | "done" | "error">("idle")
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const started = useRef(false)

  const passwordMode = Boolean(config?.passwordAuthEnabled)
  const showPasswordForm = !me && passwordMode

  useEffect(() => {
    if (loading) return
    if (!me && !passwordMode) {
      navigate(`/login?return_to=${encodeURIComponent(`/invite/${token}`)}`, { replace: true })
      return
    }
    if (!me || started.current) return
    started.current = true
    setState("accepting")
    api
      .acceptInvite(token)
      .then(async (tenant) => {
        await refresh()
        setState("done")
        toast.success(`You joined ${tenant.name}`)
        setTimeout(() => navigate(`/w/${tenant.slug}`), 900)
      })
      .catch(async (err) => {
        setState("error")
        setError(err instanceof Error ? err.message : "Could not accept invitation")
        await refresh()
      })
  }, [loading, me, token, navigate, refresh, passwordMode])

  async function createAccount(e: React.FormEvent) {
    e.preventDefault()
    if (password !== confirm) {
      setError("Passwords do not match")
      return
    }
    setBusy(true)
    setError(null)
    try {
      const result = await api.acceptInvitePassword(token, name.trim(), password)
      await refresh()
      setState("done")
      toast.success(`You joined ${result.tenant.name}`)
      setTimeout(() => navigate(`/w/${result.tenant.slug}`), 800)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not accept invitation")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen flex-col bg-background">
      <header className="flex items-center justify-between px-6 py-5 md:px-10">
        <Logo />
        {me && (
          <Button variant="ghost" size="sm" onClick={() => void logout().then(() => navigate("/login"))}>
            Switch account
          </Button>
        )}
      </header>
      <div className="flex flex-1 items-center justify-center px-6 pb-20">
        <Card className="w-full max-w-[440px]">
          <CardContent className="p-8">
            {showPasswordForm ? (
              <form onSubmit={createAccount}>
                <h1 className="text-xl font-semibold tracking-tight">Join your workspace</h1>
                <p className="mt-1.5 text-[13px] leading-relaxed text-muted-foreground">
                  Create your account with the email this invitation was sent to.
                </p>
                <div className="mt-6 space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="name">Display name <span className="font-normal text-muted-foreground">optional</span></Label>
                    <Input id="name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Maya Chen" autoComplete="name" />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="password">Password</Label>
                    <Input id="password" type="password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" placeholder="At least 8 characters" />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="confirm">Confirm password</Label>
                    <Input id="confirm" type="password" required minLength={8} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
                  </div>
                </div>
                {error && <div className="mt-4"><ErrorNotice message={error} /></div>}
                <Button type="submit" className="mt-5 w-full" disabled={busy}>
                  {busy ? <Loader2 className="size-4 animate-spin" /> : null} {busy ? "Joining…" : "Join workspace"}
                </Button>
                <div className="mt-3 text-center text-[11px] text-muted-foreground">
                  Already have an account?{" "}
                  <Link to={`/login?return_to=${encodeURIComponent(`/invite/${token}`)}`} className="font-medium text-foreground underline underline-offset-4">
                    Sign in
                  </Link>
                </div>
              </form>
            ) : state === "done" ? (
              <div className="text-center">
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-success/12 text-success">
                  <CheckCircle2 className="size-6" />
                </div>
                <h1 className="text-xl font-semibold">You’re in</h1>
                <p className="mt-2 text-sm text-muted-foreground">Taking you to your new workspace…</p>
              </div>
            ) : state === "error" ? (
              <div className="text-center">
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-destructive/10 text-destructive">
                  <MailWarning className="size-6" />
                </div>
                <h1 className="text-xl font-semibold">Invitation couldn’t be accepted</h1>
                <div className="mt-4 text-left"><ErrorNotice message={error} /></div>
                <div className="mt-5 flex justify-center gap-2">
                  <Button variant="outline" onClick={() => void logout().then(() => navigate("/login"))}>Switch account</Button>
                  <Button onClick={() => navigate("/")}>Go to workspace</Button>
                </div>
              </div>
            ) : (
              <div className="text-center">
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
                  <Loader2 className="size-6 animate-spin" />
                </div>
                <h1 className="text-xl font-semibold">Joining workspace…</h1>
                <p className="mt-2 text-sm text-muted-foreground">Verifying your invitation.</p>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
