import { useEffect, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { Loader2, LockKeyhole, ShieldCheck } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Logo } from "@/components/layout/Logo"
import { ErrorNotice } from "@/components/shared"

const sourceRows = [
  ["MongoDB", "documents"],
  ["PostgreSQL", "relational"],
  ["Redis", "keyspace"],
  ["Snowflake", "warehouse"],
  ["REST / JSON", "API"],
]

export function LoginPage() {
  const { config, me, loading, loginDev, loginPassword } = useAuth()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const returnTo = params.get("return_to") || ""
  const authError = params.get("auth_error")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function destination() {
    if (returnTo.startsWith("/") && !returnTo.startsWith("//")) return returnTo
    if (me?.tenants.length) return `/w/${me.tenants[0].slug}`
    return "/welcome"
  }

  useEffect(() => {
    if (!loading && me && !authError) navigate(destination(), { replace: true })
  }, [loading, me, authError]) // eslint-disable-line react-hooks/exhaustive-deps

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      if (config?.passwordAuthEnabled) {
        await loginPassword(email, password)
      } else {
        await loginDev(email, name)
      }
      navigate(destination(), { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sign in failed")
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return (
      <div className="grid min-h-screen place-items-center bg-background">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </div>
      </div>
    )
  }

  return (
    <div className="grid min-h-screen bg-background lg:grid-cols-[minmax(0,1.12fr)_minmax(420px,0.88fr)]">
      <div className="flex min-h-screen flex-col px-6 py-7 sm:px-10 lg:px-14 xl:px-20">
        <Logo />

        <div className="flex flex-1 items-center py-10">
          <div className="w-full max-w-[360px]">
            <h1 className="text-[28px] leading-tight font-semibold tracking-[-0.02em]">Sign in to AnyQuery</h1>
            <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
              Use your workspace account to continue.
            </p>

            {authError && <div className="mt-5"><ErrorNotice message={authError.replaceAll("+", " ")} /></div>}

            {config?.oidcEnabled && (
              <div className={authError ? "mt-5" : "mt-7"}>
                <Button asChild size="lg" className="w-full justify-center">
                  <a href={api.oidcLoginUrl(returnTo || "/")}>
                    <ShieldCheck className="size-4" /> Continue with single sign-on
                  </a>
                </Button>
                {(config.passwordAuthEnabled || config.devAuthEnabled) && (
                  <div className="my-6 flex items-center gap-3 text-[10.5px] text-muted-foreground">
                    <div className="h-px flex-1 bg-border/70" />
                    {config.passwordAuthEnabled ? "or sign in with password" : "or use development sign-in"}
                    <div className="h-px flex-1 bg-border/70" />
                  </div>
                )}
              </div>
            )}

            {(config?.passwordAuthEnabled || config?.devAuthEnabled) && (
              <form onSubmit={submit} className={config?.oidcEnabled ? "" : "mt-7"}>
                <div className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="email">Email</Label>
                    <Input
                      id="email"
                      type="email"
                      required
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="you@company.com"
                      autoComplete="email"
                    />
                  </div>
                  {config?.passwordAuthEnabled ? (
                    <div className="space-y-1.5">
                      <Label htmlFor="password">Password</Label>
                      <Input
                        id="password"
                        type="password"
                        required
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        placeholder="Your password"
                        autoComplete="current-password"
                      />
                    </div>
                  ) : (
                    <div className="space-y-1.5">
                      <Label htmlFor="name">
                        Display name <span className="font-normal text-muted-foreground">optional</span>
                      </Label>
                      <Input
                        id="name"
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                        placeholder="Maya Chen"
                        autoComplete="name"
                      />
                    </div>
                  )}
                </div>

                {error && <div className="mt-4"><ErrorNotice message={error} /></div>}

                <Button type="submit" size="lg" className="mt-5 w-full justify-center" disabled={busy || !email || (config?.passwordAuthEnabled && !password)}>
                  {busy ? <Loader2 className="size-4 animate-spin" /> : null}
                  {busy ? "Signing in…" : "Sign in"}
                </Button>

                <div className="mt-3 flex items-center justify-between text-[11px] text-muted-foreground">
                  <span>{config?.passwordAuthEnabled ? "Password login" : "Development sign-in enabled"}</span>
                  {config?.devAuthEnabled && (
                    <button
                      type="button"
                      className="font-medium text-foreground/70 underline underline-offset-4 hover:text-foreground"
                      onClick={() => {
                        setEmail("admin@anyquery.dev")
                        setPassword("anyquery-demo")
                        setName("Maya Chen")
                      }}
                    >
                      Use demo account
                    </button>
                  )}
                </div>
              </form>
            )}

            {!config?.oidcEnabled && !config?.devAuthEnabled && !config?.passwordAuthEnabled && (
              <div className="mt-6">
                <ErrorNotice
                  message="No authentication method is configured."
                  hint="Configure OIDC, or enable AUTH_PASSWORD_MODE / AUTH_DEV_MODE."
                />
              </div>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10.5px] text-muted-foreground">
          <span>Self-hosted</span>
          <span className="size-0.5 rounded-full bg-current opacity-50" />
          <span>OIDC ready</span>
          <span className="size-0.5 rounded-full bg-current opacity-50" />
          <span className="inline-flex items-center gap-1"><LockKeyhole className="size-3" /> Credentials encrypted at rest</span>
        </div>
      </div>

      <aside className="relative hidden flex-col justify-between overflow-hidden bg-[oklch(0.185_0.018_265)] px-12 py-12 text-white lg:flex xl:px-16">
        <div className="text-[10px] font-medium tracking-[0.18em] text-white/35 uppercase">Federated SQL · Self-hosted</div>

        <div className="max-w-[400px]">
          <h2 className="text-[34px] leading-[1.12] font-medium tracking-[-0.03em] text-white/95">
            One query layer.<br />Every system.
          </h2>
          <p className="mt-4 max-w-[330px] text-[12.5px] leading-[1.75] text-white/45">
            Query the databases, caches and APIs you already run. Join them without copying data into another warehouse.
          </p>

          <div className="mt-9 border-y border-white/10">
            {sourceRows.map(([name, kind]) => (
              <div key={name} className="flex items-center justify-between border-b border-white/8 py-2.5 last:border-b-0">
                <span className="font-mono text-[11px] text-white/75">{name}</span>
                <span className="text-[10px] tracking-wide text-white/30">{kind}</span>
              </div>
            ))}
          </div>
        </div>

        <div className="flex items-center justify-between text-[10px] text-white/25">
          <span>AnyQuery MVP</span>
          <span>31 connectors · RBAC · Audit</span>
        </div>
      </aside>
    </div>
  )
}
