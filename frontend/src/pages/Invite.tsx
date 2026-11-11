import { useEffect, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { CheckCircle2, Loader2, MailWarning } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Logo } from "@/components/layout/Logo"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { ErrorNotice } from "@/components/shared"
import { toast } from "sonner"

export function InvitePage() {
  const { token = "" } = useParams()
  const { me, loading, refresh, logout, tenantFor } = useAuth()
  const navigate = useNavigate()
  const [state, setState] = useState<"idle" | "accepting" | "done" | "error">("idle")
  const [error, setError] = useState<string | null>(null)
  const started = useRef(false)

  useEffect(() => {
    if (loading) return
    if (!me) {
      navigate(`/login?return_to=${encodeURIComponent(`/invite/${token}`)}`, { replace: true })
      return
    }
    if (started.current) return
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
  }, [loading, me, token, navigate, refresh])

  return (
    <div className="relative flex min-h-screen flex-col bg-background">
      <div className="absolute inset-0 dot-bg opacity-40" />
      <header className="relative flex items-center justify-between px-6 py-5 md:px-10">
        <Logo />
        {me && (
          <Button variant="ghost" size="sm" onClick={() => void logout().then(() => navigate("/login"))}>
            Switch account
          </Button>
        )}
      </header>
      <div className="relative flex flex-1 items-center justify-center px-6 pb-20">
        <Card className="w-full max-w-[440px]">
          <CardContent className="p-8 text-center">
            {state === "done" ? (
              <>
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-success/12 text-success">
                  <CheckCircle2 className="size-6" />
                </div>
                <h1 className="text-xl font-semibold">You’re in</h1>
                <p className="mt-2 text-sm text-muted-foreground">Taking you to your new workspace…</p>
              </>
            ) : state === "error" ? (
              <>
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-destructive/10 text-destructive">
                  <MailWarning className="size-6" />
                </div>
                <h1 className="text-xl font-semibold">Invitation couldn’t be accepted</h1>
                <div className="mt-4 text-left"><ErrorNotice message={error} /></div>
                <p className="mt-4 text-xs text-muted-foreground">Signed in as {me?.user.email}</p>
                <div className="mt-5 flex justify-center gap-2">
                  <Button variant="outline" onClick={() => void logout().then(() => navigate("/login"))}>Switch account</Button>
                  <Button onClick={() => navigate(tenantFor(me?.tenants[0]?.slug || "") ? `/w/${me!.tenants[0].slug}` : "/welcome")}>Go to workspace</Button>
                </div>
              </>
            ) : (
              <>
                <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
                  <Loader2 className="size-6 animate-spin" />
                </div>
                <h1 className="text-xl font-semibold">Joining workspace…</h1>
                <p className="mt-2 text-sm text-muted-foreground">Verifying your invitation.</p>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
