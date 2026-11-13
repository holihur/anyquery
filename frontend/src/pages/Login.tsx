import { useEffect, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { Loader2, LockKeyhole, Network, ShieldCheck } from "lucide-react"
import { api } from "@/lib/api"
import { useAuth } from "@/providers/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Logo } from "@/components/layout/Logo"
import { SourceIcon } from "@/components/SourceIcon"
import { ErrorNotice } from "@/components/shared"
import { LanguageMenu } from "@/components/layout/LanguageMenu"
import { useI18n } from "@/i18n"

const topologySources = [
  { type: "mongodb", name: "MongoDB", kind: "documents", color: "text-emerald-400" },
  { type: "postgres", name: "PostgreSQL", kind: "relational", color: "text-sky-400" },
  { type: "redis", name: "Redis", kind: "keyspace", color: "text-red-400" },
  { type: "snowflake", name: "Snowflake", kind: "warehouse", color: "text-cyan-400" },
  { type: "graphql", name: "GraphQL", kind: "API", color: "text-pink-400" },
  { type: "rest", name: "REST / JSON", kind: "API", color: "text-teal-400" },
]

const moreSources = ["sqlite", "bigquery", "clickhouse", "github", "elasticsearch", "csv"]

function SourceNode({ type, name, kind, color, index = 0 }: { type: string; name: string; kind: string; color: string; index?: number }) {
  return (
    <div
      className="aq-login-node group flex min-w-0 flex-col items-center gap-2 rounded-md border border-white/10 bg-white/[0.035] px-1.5 py-2.5 text-center transition-colors hover:border-white/15 hover:bg-white/[0.055]"
      style={{ animationDelay: `${80 + index * 55}ms` }}
    >
      <SourceIcon
        type={type}
        size="lg"
        className="size-9 rounded-md border border-white/10 bg-white/[0.055] transition-transform duration-300 group-hover:-translate-y-0.5"
        glyphClassName={color}
      />
      <div className="min-w-0">
        <div className="truncate font-mono text-[10px] text-white/75">{name}</div>
        <div className="truncate text-[8.5px] tracking-[0.08em] text-white/28 uppercase">{kind}</div>
      </div>
    </div>
  )
}

export function LoginPage() {
  const { t } = useI18n()
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
          <Loader2 className="size-4 animate-spin" /> {t("Loading…")}
        </div>
      </div>
    )
  }

  return (
    <div className="grid min-h-screen bg-background lg:grid-cols-[minmax(0,1.12fr)_minmax(420px,0.88fr)]">
      <div className="flex min-h-screen flex-col px-6 py-7 sm:px-10 lg:px-14 xl:px-20">
        <div className="flex items-center justify-between">
          <Logo />
          <LanguageMenu compact />
        </div>

        <div className="flex flex-1 items-center py-10">
          <div className="w-full max-w-[360px]">
            <h1 className="text-[28px] leading-tight font-semibold tracking-[-0.02em]">{t("Sign in to AnyQuery")}</h1>
            <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
              {t("Use your workspace account to continue.")}
            </p>

            {authError && <div className="mt-5"><ErrorNotice message={authError.replaceAll("+", " ")} /></div>}

            {config?.oidcEnabled && (
              <div className={authError ? "mt-5" : "mt-7"}>
                <Button asChild size="lg" className="w-full justify-center">
                  <a href={api.oidcLoginUrl(returnTo || "/")}>
                    <ShieldCheck className="size-4" /> {t("Continue with single sign-on")}
                  </a>
                </Button>
                {(config.passwordAuthEnabled || config.devAuthEnabled) && (
                  <div className="my-6 flex items-center gap-3 text-[10.5px] text-muted-foreground">
                    <div className="h-px flex-1 bg-border/70" />
                    {config.passwordAuthEnabled ? t("or sign in with password") : t("or use development sign-in")}
                    <div className="h-px flex-1 bg-border/70" />
                  </div>
                )}
              </div>
            )}

            {(config?.passwordAuthEnabled || config?.devAuthEnabled) && (
              <form onSubmit={submit} className={config?.oidcEnabled ? "" : "mt-7"}>
                <div className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="email">{t("Email")}</Label>
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
                      <Label htmlFor="password">{t("Password")}</Label>
                      <Input
                        id="password"
                        type="password"
                        required
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        placeholder={t("Your password")}
                        autoComplete="current-password"
                      />
                    </div>
                  ) : (
                    <div className="space-y-1.5">
                      <Label htmlFor="name">
                        {t("Display name")} <span className="font-normal text-muted-foreground">{t("optional")}</span>
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
                  {busy ? t("Signing in…") : t("Sign in")}
                </Button>

                <div className="mt-3 flex items-center justify-between text-[11px] text-muted-foreground">
                  <span>{config?.passwordAuthEnabled ? t("Password login") : t("Development sign-in enabled")}</span>
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
                      {t("Use demo account")}
                    </button>
                  )}
                </div>
              </form>
            )}

            {!config?.oidcEnabled && !config?.devAuthEnabled && !config?.passwordAuthEnabled && (
              <div className="mt-6">
                <ErrorNotice
                  message={t("No authentication method is configured.")}
                  hint={t("Configure OIDC, or enable AUTH_PASSWORD_MODE / AUTH_DEV_MODE.")}
                />
              </div>
            )}

            <div className="mt-9 flex items-center gap-1.5 lg:hidden">
              {topologySources.map((source) => (
                <SourceIcon
                  key={source.type}
                  type={source.type}
                  size="sm"
                  className="size-7"
                  glyphClassName={source.color}
                  title={source.name}
                />
              ))}
              <span className="ml-1 text-[10px] text-muted-foreground">104 data sources</span>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10.5px] text-muted-foreground">
          <span>{t("Self-hosted")}</span>
          <span className="size-0.5 rounded-full bg-current opacity-50" />
          <span>{t("OIDC ready")}</span>
          <span className="size-0.5 rounded-full bg-current opacity-50" />
          <span className="inline-flex items-center gap-1"><LockKeyhole className="size-3" /> {t("Credentials encrypted at rest")}</span>
        </div>
      </div>

      <aside className="relative hidden flex-col justify-between overflow-hidden bg-[oklch(0.185_0.018_265)] px-12 py-12 text-white lg:flex xl:px-16">
        <div className="text-[10px] font-medium tracking-[0.18em] text-white/35 uppercase">Federated SQL · Self-hosted</div>

        <div className="max-w-[400px]">
          <h2 className="text-[34px] leading-[1.12] font-medium tracking-[-0.03em] text-white/95">
            {t("One query layer.")}<br />{t("Every system.")}
          </h2>
          <p className="mt-4 max-w-[330px] text-[12.5px] leading-[1.75] text-white/45">
            {t("Query the databases, caches and APIs you already run. Join them without copying data into another warehouse.")}
          </p>

          <div className="mt-8">
            <div className="grid grid-cols-3 gap-2">
              {topologySources.slice(0, 3).map((source, index) => <SourceNode key={source.type} {...source} index={index} />)}
            </div>

            <div className="relative my-1.5 h-14">
              <svg className="absolute inset-0 size-full" viewBox="0 0 300 56" fill="none" preserveAspectRatio="none" aria-hidden>
                <path className="aq-login-flow" d="M50 0V14C50 21 70 20 82 28H134" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" />
                <path className="aq-login-flow" d="M150 0V18" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" style={{ animationDelay: "-0.45s" }} />
                <path className="aq-login-flow" d="M250 0V14C250 21 230 20 218 28H166" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" style={{ animationDelay: "-0.9s" }} />
                <path className="aq-login-flow" d="M50 56V42C50 35 70 36 82 28H134" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" style={{ animationDelay: "-1.35s" }} />
                <path className="aq-login-flow" d="M150 56V38" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" style={{ animationDelay: "-1.8s" }} />
                <path className="aq-login-flow" d="M250 56V42C250 35 230 36 218 28H166" stroke="currentColor" strokeOpacity=".22" vectorEffect="non-scaling-stroke" style={{ animationDelay: "-2.25s" }} />
              </svg>
              <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2">
                <div className="relative flex items-center gap-1.5 rounded-md border border-white/15 bg-[oklch(0.185_0.018_265)] px-2.5 py-1.5 font-mono text-[9px] tracking-[0.08em] text-white/60 shadow-sm">
                  <span className="aq-login-hub-pulse pointer-events-none absolute -inset-px rounded-md border border-emerald-400/50" />
                  <Network className="size-3 text-emerald-400/80" />
                  <span className="aq-login-dot size-1 rounded-full bg-emerald-400" />
                  FEDERATED SQL
                </div>
              </div>
            </div>

            <div className="grid grid-cols-3 gap-2">
              {topologySources.slice(3).map((source, index) => <SourceNode key={source.type} {...source} index={index + 3} />)}
            </div>

            <div className="mt-4 flex items-center justify-between border-t border-white/8 pt-3">
              <span className="text-[8.5px] font-medium tracking-[0.16em] text-white/25 uppercase">Also connects</span>
              <div className="flex items-center gap-1">
                {moreSources.map((type, index) => (
                  <SourceIcon
                    key={type}
                    type={type}
                    size="sm"
                    className="size-6 rounded border border-white/8 bg-white/[0.035]"
                    glyphClassName="text-white/55"
                    title={type}
                  />
                ))}
                <span className="ml-1 font-mono text-[9px] text-white/25">+98</span>
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between text-[10px] text-white/25">
          <span>AnyQuery MVP</span>
          <span>104 connectors · RBAC · Audit</span>
        </div>
      </aside>
    </div>
  )
}
