export type Role = "owner" | "admin" | "member" | "viewer"

export interface User {
  id: string
  email: string
  name: string
  avatarUrl?: string
  createdAt: string
  lastLoginAt: string
}

export interface Tenant {
  id: string
  slug: string
  name: string
  createdBy: string
  createdAt: string
  updatedAt: string
}

export interface TenantRole extends Tenant {
  role: Role
}

export interface AuthConfig {
  oidcEnabled: boolean
  passwordAuthEnabled: boolean
  devAuthEnabled: boolean
  appName: string
  issuer?: string
  sessionHours: number
}

export interface AuthMe {
  user: User
  tenants: TenantRole[]
  authType: "session" | "api_key"
}

export interface Column {
  name: string
  type: string
  nullable?: boolean
}

export interface Table {
  name: string
  displayName?: string
  schema?: string
  kind: string
  columns: Column[]
}

export interface DataSource {
  id: string
  name: string
  type: string
  typeName: string
  icon?: string
  config: Record<string, string>
  tables: Table[]
  status: "ready" | "error" | string
  lastError?: string
  createdAt: string
  updatedAt: string
  lastSyncedAt?: string
}

export interface ConnectorField {
  key: string
  label: string
  kind: "text" | "password" | "number" | "textarea" | "select" | "bool"
  required: boolean
  secret: boolean
  placeholder?: string
  help?: string
  default?: string
  options?: { value: string; label: string }[]
}

export interface ConnectorType {
  type: string
  name: string
  category: string
  description: string
  icon: string
  maturity?: "native" | "compatible" | "template" | string
  fields: ConnectorField[]
}

export interface ResultColumn {
  name: string
  type: string
}

export interface SourceStat {
  source: string
  table: string
  rows: number
  durationMs: number
}

export interface QueryResult {
  columns: ResultColumn[]
  rows: Record<string, unknown>[]
  rowCount: number
  truncated: boolean
  warnings?: string[]
  stats: {
    durationMs: number
    sources: SourceStat[]
    scannedRows: number
    stages: string[]
  }
}

export interface QueryRun {
  id: string
  tenantId: string
  userId: string
  savedQueryId?: string
  sql: string
  status: "success" | "error"
  durationMs: number
  rowCount: number
  error?: string
  sources: string[]
  createdAt: string
}

export interface ExecuteResponse {
  result?: QueryResult
  run?: QueryRun
  error?: { code: string; message: string; hint?: string }
}

export interface SavedQuery {
  id: string
  tenantId: string
  name: string
  description?: string
  sql: string
  visibility: "team" | "private"
  createdBy: string
  createdAt: string
  updatedAt: string
  lastRunAt?: string
}

export interface Member {
  user: User
  role: Role
  createdAt: string
}

export interface Invite {
  id: string
  tenantId: string
  email: string
  role: Role
  invitedBy: string
  createdAt: string
  expiresAt: string
  acceptedAt?: string
}

export interface AuditLog {
  id: string
  tenantId: string
  actorId: string
  actorName: string
  action: string
  targetType?: string
  targetId?: string
  metadata?: Record<string, unknown>
  ip?: string
  createdAt: string
}

export interface APIKey {
  id: string
  tenantId: string
  name: string
  prefix: string
  scopes: string[]
  createdBy: string
  createdAt: string
  lastUsedAt?: string
  revokedAt?: string
}

export interface Overview {
  dataSources: number
  readyDataSources: number
  tables: number
  members: number
  savedQueries: number
  recentRuns: QueryRun[]
}
