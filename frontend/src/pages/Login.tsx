import { useEffect, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { ArrowRight, KeyRound, Loader2, LockKeyhole, ShieldCheck, Sparkles } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Logo } from "@/components/layout/Logo"
import { ErrorNotice } from "@/components/shared"

export function LoginPage() {
  const { config, me, loading, loginDev } = useAuth()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const returnTo = params.get("return_to") || ""
  const authError = params.get("auth_error")
  const [email, setEmail] = useState("admin@anyquery.dev")
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
      await loginDev(email, name)
      navigate(destination(), { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sign in failed")
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return (
      <div className="grid min-h-screen place-items-center">
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> Loading…</div>
      </div>
    )
  }

  return (
    <div className="grid min-h-screen lg:grid-cols-[1.05fr_1fr]">
      <div className="flex flex-col px-6 py-8 sm:px-10 lg:px-16">
        <Logo />
        <div className="flex flex-1 items-center py-12">
          <div className="w-full max-w-[380px]">
            <div className="mb-8">
              <div className="mb-3 inline-flex items-center gap-1.5 rounded-full border bg-muted/40 px-2.5 py-1 text-[11px] font-medium text-muted-foreground">
                <Sparkles className="size-3 text-primary" /> Federated SQL workspace
              </div>
              <h1 className="text-[26px] leading-tight font-semibold tracking-tight">Welcome back</h1>
              <p className="mt-2 text-[13.5px] leading-relaxed text-muted-foreground">
                Sign in to query MongoDB, Postgres, APIs and 20+ sources with one SQL workspace.
              </p>
            </div>

            {authError && <ErrorNotice message={authError.replaceAll("+", " ")} />}

            {config?.oidcEnabled && (
              <div className={authError ? "mt-5" : ""}>
                <Button asChild size="lg" className="w-full">
                  <a href={api.oidcLoginUrl(returnTo || "/")}>
                    <ShieldCheck className="size-4" /> Continue with single sign-on
                  </a>
                </Button>
                {config.devAuthEnabled && (
                  <div className="my-6 flex items-center gap-3 text-[11px] text-muted-foreground">
                    <div className="h-px flex-1 bg-border" /> or use development login <div className="h-px flex-1 bg-border" />
                  </div>
                )}
              </div>
            )}

            {config?.devAuthEnabled && (
              <form onSubmit={submit} className={config?.oidcEnabled ? "" : "mt-2"}>
                <div className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="email">Email</Label>
                    <Input id="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@company.com" autoComplete="email" />
                  </div>
                  <div className="space-y-1.5">
                    <div className="flex items-center justify-between">
                      <Label htmlFor="name">Display name <span className="font-normal text-muted-foreground">(optional)</span></Label>
                    </div>
                    <Input id="name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Maya Chen" autoComplete="name" />
                  </div>
                </div>
                {error && <div className="mt-4"><ErrorNotice message={error} /></div>}
                <Button type="submit" size="lg" className="mt-5 w-full" disabled={busy || !email}>
                  {busy ? <Loader2 className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  {busy ? "Signing in…" : "Sign in to workspace"}
                </Button>
                <div className="mt-3 flex items-center justify-between text-[11px] text-muted-foreground">
                  <span className="inline-flex items-center gap-1"><LockKeyhole className="size-3" /> Development mode</span>
                  <button type="button" className="font-medium text-primary hover:underline" onClick={() => { setEmail("admin@anyquery.dev"); setName("Maya Chen") }}>
                    Use demo admin
                  </button>
                </div>
              </form>
            )}

            {!config?.oidcEnabled && !config?.devAuthEnabled && (
              <ErrorNotice message="No authentication method is configured." hint="Set OIDC_ISSUER / OIDC_CLIENT_ID or enable AUTH_DEV_MODE." />
            )}

            <div className="mt-8 flex items-center gap-2 text-[11px] text-muted-foreground">
              <LockKeyhole className="size-3" /> Sessions are stored server-side. Data source credentials are encrypted at rest.
            </div>
          </div>
        </div>
        <div className="text-[11px] text-muted-foreground">AnyQuery · self-hosted · OIDC ready</div>
      </div>

      <div className="relative hidden overflow-hidden border-l bg-[oklch(0.17_0.02_265)] p-10 text-white lg:flex lg:flex-col lg:justify-center">
        <div className="absolute inset-0 dot-bg opacity-[0.09]" />
        <div className="absolute -top-32 -right-24 size-[420px] rounded-full bg-indigo-500/25 blur-[100px]" />
        <div className="absolute -bottom-24 -left-16 size-[360px] rounded-full bg-cyan-400/15 blur-[100px]" />
        <div className="relative mx-auto w-full max-w-[480px]">
          <div className="mb-6 flex items-center gap-2 text-[11px] font-medium tracking-[0.16em] text-white/45 uppercase">
            <span className="size-1.5 rounded-full bg-emerald-400" /> Query engine connected
          </div>
          <div className="overflow-hidden rounded-2xl border border-white/10 bg-white/[0.045] shadow-2xl backdrop-blur">
            <div className="flex items-center gap-2 border-b border-white/8 px-4 py-3">
              <span className="size-2 rounded-full bg-red-400/80" />
              <span className="size-2 rounded-full bg-amber-400/80" />
              <span className="size-2 rounded-full bg-emerald-400/80" />
              <span className="ml-2 font-mono text-[10px] text-white/35">revenue_by_category.sql</span>
            </div>
            <pre className="overflow-hidden px-5 py-5 font-mono text-[11.5px] leading-[1.9] text-white/80">
              <span className="text-indigo-300">SELECT</span> p.category,{`\n`}
              {"       "}<span className="text-cyan-300">count</span>(*) <span className="text-indigo-300">AS</span> orders,{`\n`}
              {"       "}<span className="text-cyan-300">round</span>(<span className="text-cyan-300">sum</span>(o.total), 2) <span className="text-indigo-300">AS</span> revenue{`\n`}
              <span className="text-indigo-300">FROM</span> <span className="text-emerald-300">mongo.orders</span> o{`\n`}
              <span className="text-indigo-300">JOIN</span> <span className="text-amber-300">shop.products</span> p{`\n`}
              {"  "}<span className="text-indigo-300">ON</span> o.product_id = p.id{`\n`}
              <span className="text-indigo-300">WHERE</span> o.status = <span className="text-rose-300">'paid'</span>{`\n`}
              <span className="text-indigo-300">GROUP BY</span> p.category
            </pre>
            <div className="grid grid-cols-3 gap-px border-t border-white/8 bg-white/5">
              {[
                ["6", "categories"],
                ["17180", "revenue"],
                ["145 ms", "federated"],
              ].map(([value, label]) => (
                <div key={label} className="bg-[oklch(0.19_0.02_265)]/80 px-4 py-3">
                  <div className="text-sm font-semibold text-white">{value}</div>
                  <div className="text-[10px] text-white/40">{label}</div>
                </div>
              ))}
            </div>
          </div>
          <div className="mt-8 flex flex-wrap gap-2">
            {["MongoDB", "PostgreSQL", "MySQL", "Snowflake", "REST APIs", "Redis", "JSONL"].map((s) => (
              <span key={s} className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[10.5px] text-white/55">{s}</span>
            ))}
          </div>
          <p className="mt-8 max-w-sm text-[13px] leading-relaxed text-white/45">
            One SQL dialect. Cross-source joins. Team workspaces, OIDC, schema discovery and a query history your whole team can build on.
          </p>
          <div className="mt-5 flex items-center gap-1.5 text-[11px] text-white/40">
            <ArrowRight className="size-3" /> Built for teams who live in data
          </div>
        </div>
      </div>
    </div>
  )
}
