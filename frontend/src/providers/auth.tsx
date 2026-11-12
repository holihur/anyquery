import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { ApiError, api } from "@/lib/api"
import type { AuthConfig, AuthMe, TenantRole } from "@/lib/types"

interface AuthContextValue {
  config: AuthConfig | null
  me: AuthMe | null
  loading: boolean
  error: string | null
  refresh: () => Promise<AuthMe | null>
  loginDev: (email: string, name?: string) => Promise<void>
  loginPassword: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  tenantFor: (slug: string) => TenantRole | undefined
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = useState<AuthConfig | null>(null)
  const [me, setMe] = useState<AuthMe | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const next = await api.me()
      setMe(next)
      return next
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setMe(null)
        return null
      }
      setError(err instanceof Error ? err.message : "Could not reach the server")
      return null
    }
  }, [])

  useEffect(() => {
    let alive = true
    async function boot() {
      try {
        const cfg = await api.authConfig()
        if (!alive) return
        setConfig(cfg)
        await refresh()
      } catch (err) {
        if (alive) setError(err instanceof Error ? err.message : "Could not reach the server")
      } finally {
        if (alive) setLoading(false)
      }
    }
    void boot()
    return () => {
      alive = false
    }
  }, [refresh])

  const loginDev = useCallback(async (email: string, name?: string) => {
    await api.devLogin(email, name)
    await refresh()
  }, [refresh])

  const loginPassword = useCallback(async (email: string, password: string) => {
    await api.passwordLogin(email, password)
    await refresh()
  }, [refresh])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setMe(null)
    }
  }, [])

  const tenantFor = useCallback(
    (slug: string) => me?.tenants.find((t) => t.slug === slug || t.id === slug),
    [me],
  )

  const value = useMemo<AuthContextValue>(
    () => ({ config, me, loading, error, refresh, loginDev, loginPassword, logout, tenantFor }),
    [config, me, loading, error, refresh, loginDev, loginPassword, logout, tenantFor],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider")
  return ctx
}
