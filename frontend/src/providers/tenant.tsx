import { createContext, useContext } from "react"
import { Link, useParams } from "react-router-dom"
import { useAuth } from "@/providers/auth"
import type { Role, TenantRole } from "@/lib/types"
import { Button } from "@/components/ui/button"

interface TenantContextValue {
  tenant: TenantRole
  role: Role
  slug: string
}

const TenantContext = createContext<TenantContextValue | null>(null)

export function TenantProvider({ children }: { children: React.ReactNode }) {
  const { slug = "" } = useParams()
  const { tenantFor, loading } = useAuth()
  const tenant = tenantFor(slug)

  if (!tenant) {
    if (loading) return <div className="grid h-screen place-items-center text-sm text-muted-foreground">Loading workspace…</div>
    return (
      <div className="grid h-screen place-items-center px-6">
        <div className="max-w-md text-center">
          <div className="mx-auto mb-4 grid size-12 place-items-center rounded-2xl border bg-muted text-xl">🔒</div>
          <h1 className="text-lg font-semibold">Workspace not available</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            This workspace does not exist, or your account is not a member.
          </p>
          <Button asChild className="mt-5">
            <Link to="/">Back to your workspaces</Link>
          </Button>
        </div>
      </div>
    )
  }

  return <TenantContext.Provider value={{ tenant, role: tenant.role, slug: tenant.slug }}>{children}</TenantContext.Provider>
}

export function useTenant() {
  const ctx = useContext(TenantContext)
  if (!ctx) throw new Error("useTenant must be used inside TenantProvider")
  return ctx
}
