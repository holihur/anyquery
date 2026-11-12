import type {
  APIKey,
  AuditLog,
  AuthConfig,
  AuthMe,
  ConnectorType,
  DataSource,
  ExecuteResponse,
  Invite,
  Member,
  Overview,
  QueryRun,
  Role,
  SavedQuery,
  Tenant,
  TenantRole,
} from "./types"

const BASE = "/api/v1"

export class ApiError extends Error {
  status: number
  code: string
  hint?: string
  payload?: unknown

  constructor(status: number, code: string, message: string, hint?: string, payload?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.hint = hint
    this.payload = payload
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    credentials: "include",
    ...init,
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  })
  const text = await res.text()
  let payload: any = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }
  if (!res.ok) {
    const err = payload?.error
    throw new ApiError(
      res.status,
      err?.code || "request_failed",
      err?.message || `Request failed (${res.status})`,
      err?.hint,
      payload,
    )
  }
  return payload as T
}

export const api = {
  authConfig: () => request<AuthConfig>("/auth/config"),
  me: () => request<AuthMe>("/auth/me"),
  devLogin: (email: string, name?: string) =>
    request<{ user: AuthMe["user"] }>("/auth/dev-login", {
      method: "POST",
      body: JSON.stringify({ email, name }),
    }),
  passwordLogin: (email: string, password: string) =>
    request<{ user: AuthMe["user"] }>("/auth/password-login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  resetMemberPassword: (slug: string, userId: string) =>
    request<{ password: string }>(`/tenants/${slug}/members/${userId}/password`, { method: "POST" }),
  acceptInvitePassword: (token: string, name: string, password: string) =>
    request<{ tenant: Tenant; role: Role; user: AuthMe["user"] }>("/invites/accept-password", {
      method: "POST",
      body: JSON.stringify({ token, name, password }),
    }),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  oidcLoginUrl: (returnTo?: string) =>
    `${BASE}/auth/oidc/login${returnTo ? `?return_to=${encodeURIComponent(returnTo)}` : ""}`,

  createTenant: (name: string, slug?: string) =>
    request<TenantRole>("/tenants", { method: "POST", body: JSON.stringify({ name, slug }) }),
  tenant: (slug: string) =>
    request<{ tenant: Tenant; role: Role; limits: { maxResultRows: number; queryTimeoutSeconds: number; maxSavedQueries: number }; usage: { savedQueries: number } }>(`/tenants/${slug}`),
  updateTenant: (slug: string, name: string) =>
    request<Tenant>(`/tenants/${slug}`, { method: "PATCH", body: JSON.stringify({ name }) }),
  overview: (slug: string) => request<Overview>(`/tenants/${slug}/overview`),

  members: (slug: string) =>
    request<{ members: Member[]; invites?: Invite[]; role: Role }>(`/tenants/${slug}/members`),
  updateMemberRole: (slug: string, userId: string, role: Role) =>
    request<{ ok: boolean }>(`/tenants/${slug}/members/${userId}`, {
      method: "PATCH",
      body: JSON.stringify({ role }),
    }),
  removeMember: (slug: string, userId: string) =>
    request<void>(`/tenants/${slug}/members/${userId}`, { method: "DELETE" }),
  invite: (slug: string, email: string, role: Role) =>
    request<{ invite: Invite; link: string; token: string }>(`/tenants/${slug}/invites`, {
      method: "POST",
      body: JSON.stringify({ email, role }),
    }),
  revokeInvite: (slug: string, id: string) =>
    request<void>(`/tenants/${slug}/invites/${id}`, { method: "DELETE" }),
  acceptInvite: (token: string) =>
    request<TenantRole>("/invites/accept", { method: "POST", body: JSON.stringify({ token }) }),

  connectorTypes: () => request<{ types: ConnectorType[] }>("/datasource-types"),
  dataSources: (slug: string) => request<{ dataSources: DataSource[] }>(`/tenants/${slug}/datasources`),
  createDataSource: (slug: string, name: string, type: string, config: Record<string, string>) =>
    request<DataSource>(`/tenants/${slug}/datasources`, {
      method: "POST",
      body: JSON.stringify({ name, type, config }),
    }),
  updateDataSource: (slug: string, id: string, name: string, config: Record<string, string>) =>
    request<DataSource>(`/tenants/${slug}/datasources/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ name, config }),
    }),
  deleteDataSource: (slug: string, id: string) =>
    request<void>(`/tenants/${slug}/datasources/${id}`, { method: "DELETE" }),
  testDataSource: (slug: string, id: string) =>
    request<{ ok: boolean; latencyMs?: number; error?: string }>(`/tenants/${slug}/datasources/${id}/test`, {
      method: "POST",
    }),
  testDataSourceConfig: (slug: string, type: string, config: Record<string, string>, datasourceId?: string) =>
    request<{ ok: boolean; latencyMs?: number; error?: string }>(`/tenants/${slug}/datasources/test`, {
      method: "POST",
      body: JSON.stringify({ type, config, datasourceId }),
    }),
  refreshDataSource: (slug: string, id: string) =>
    request<DataSource>(`/tenants/${slug}/datasources/${id}/refresh`, { method: "POST" }),
  catalog: (slug: string) =>
    request<{ sources: { name: string; type: string; status: string; error?: string; tables: DataSource["tables"] }[] }>(
      `/tenants/${slug}/catalog`,
    ),

  execute: async (slug: string, sql: string, savedQueryId?: string): Promise<ExecuteResponse> => {
    const res = await fetch(`${BASE}/tenants/${slug}/queries/execute`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sql, savedQueryId }),
    })
    const text = await res.text()
    const payload = text ? JSON.parse(text) : {}
    if (!res.ok && !payload?.error) {
      throw new ApiError(res.status, "request_failed", `Request failed (${res.status})`)
    }
    return payload as ExecuteResponse
  },

  savedQueries: (slug: string) => request<{ queries: SavedQuery[] }>(`/tenants/${slug}/queries`),
  savedQuery: (slug: string, id: string) => request<SavedQuery>(`/tenants/${slug}/queries/${id}`),
  createSavedQuery: (
    slug: string,
    query: { name: string; description?: string; sql: string; visibility: "team" | "private" },
  ) =>
    request<SavedQuery>(`/tenants/${slug}/queries`, {
      method: "POST",
      body: JSON.stringify(query),
    }),
  updateSavedQuery: (
    slug: string,
    id: string,
    query: { name: string; description?: string; sql: string; visibility: "team" | "private" },
  ) =>
    request<SavedQuery>(`/tenants/${slug}/queries/${id}`, {
      method: "PATCH",
      body: JSON.stringify(query),
    }),
  deleteSavedQuery: (slug: string, id: string) =>
    request<void>(`/tenants/${slug}/queries/${id}`, { method: "DELETE" }),

  runs: (slug: string, limit = 50) => request<{ runs: QueryRun[] }>(`/tenants/${slug}/runs?limit=${limit}`),
  audit: (slug: string, limit = 100) => request<{ logs: AuditLog[] }>(`/tenants/${slug}/audit?limit=${limit}`),

  apiKeys: (slug: string) => request<{ apiKeys: APIKey[] }>(`/tenants/${slug}/apikeys`),
  createApiKey: (slug: string, name: string, scopes: string[]) =>
    request<{ key: string; apiKey: APIKey }>(`/tenants/${slug}/apikeys`, {
      method: "POST",
      body: JSON.stringify({ name, scopes }),
    }),
  revokeApiKey: (slug: string, id: string) =>
    request<void>(`/tenants/${slug}/apikeys/${id}`, { method: "DELETE" }),
}
