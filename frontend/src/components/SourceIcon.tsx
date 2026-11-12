import {
  Activity,
  Boxes,
  Braces,
  Cloud,
  Database,
  FileSpreadsheet,
  Globe2,
  Handshake,
  Headphones,
  Layers,
  Mail,
  MessageSquare,
  Network,
  Search,
  Share2,
  Snowflake,
  Zap,
  type LucideIcon,
} from "lucide-react"
import {
  siAirtable, siApachecassandra, siApachesolr, siAsana, siClickup, siCockroachlabs,
  siClickhouse, siDatabricks, siDatadog, siDiscord, siElasticsearch, siGithub,
  siGitlab, siGoogleanalytics, siGooglebigquery, siGooglecloud, siGooglesheets,
  siGreptimedb, siHubspot, siInfluxdb, siIntercom, siJira, siLinear, siMailchimp,
  siMariadb, siMixpanel, siMongodb, siMysql, siNeo4j, siNeon, siNotion,
  siOpensearch, siPagerduty, siPlanetscale, siPostgresql, siPresto, siRedis,
  siSentry, siShopify, siSinglestore, siSnowflake, siStripe, siSupabase,
  siTelegram, siTimescale, siTrello, siTrino, siVitess, siX, siZendesk,
  type SimpleIcon,
} from "simple-icons"
import { faAws, faGoogle, faMicrosoft, faSalesforce, faSlack, type IconDefinition } from "@fortawesome/free-brands-svg-icons"
import { cn } from "@/lib/utils"

type SourceIconMeta = {
  icon?: LucideIcon
  brand?: SimpleIcon
  fa?: IconDefinition
  color: string
  bg: string
  label: string
}

const typeMeta: Record<string, SourceIconMeta> = {
  mongodb: { icon: Database, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "MongoDB" },
  redis: { icon: Layers, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Redis" },
  postgres: { icon: Database, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "PostgreSQL" },
  cockroachdb: { icon: Database, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "CockroachDB" },
  redshift: { icon: Database, color: "text-rose-600 dark:text-rose-400", bg: "bg-rose-500/10", label: "Redshift" },
  timescaledb: { icon: Activity, color: "text-amber-600 dark:text-amber-400", bg: "bg-amber-500/10", label: "TimescaleDB" },
  influxdb: { icon: Activity, color: "text-pink-600 dark:text-pink-400", bg: "bg-pink-500/10", label: "InfluxDB" },
  questdb: { icon: Activity, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "QuestDB" },
  greptimedb: { icon: Activity, color: "text-lime-600 dark:text-lime-400", bg: "bg-lime-500/10", label: "GreptimeDB" },
  sls: { icon: Cloud, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "Alibaba Cloud SLS" },
  tdengine: { icon: Activity, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "TDengine" },
  druid: { icon: Activity, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Apache Druid" },
  excel: { icon: FileSpreadsheet, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "Microsoft Excel" },
  access: { icon: Database, color: "text-blue-700 dark:text-blue-400", bg: "bg-blue-500/10", label: "Microsoft Access" },
  yugabytedb: { icon: Database, color: "text-lime-600 dark:text-lime-400", bg: "bg-lime-500/10", label: "YugabyteDB" },
  mysql: { icon: Database, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "MySQL" },
  mariadb: { icon: Database, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "MariaDB" },
  singlestore: { icon: Database, color: "text-fuchsia-600 dark:text-fuchsia-400", bg: "bg-fuchsia-500/10", label: "SingleStore" },
  sqlserver: { icon: Database, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "SQL Server" },
  sqlite: { icon: Database, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "SQLite" },
  clickhouse: { icon: Zap, color: "text-yellow-500", bg: "bg-yellow-500/10", label: "ClickHouse" },
  trino: { icon: Network, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Trino" },
  presto: { icon: Network, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "Presto" },
  snowflake: { icon: Snowflake, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10", label: "Snowflake" },
  databricks: { icon: Boxes, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Databricks" },
  oracle: { icon: Database, color: "text-red-700 dark:text-red-400", bg: "bg-red-500/10", label: "Oracle" },
  elasticsearch: { icon: Search, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "Elasticsearch" },
  opensearch: { icon: Search, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "OpenSearch" },
  rest: { icon: Globe2, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "REST / JSON" },
  csv: { icon: FileSpreadsheet, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "CSV" },
  jsonl: { icon: Braces, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "JSON Lines" },
  twilio: { icon: MessageSquare, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Twilio" },
  sendgrid: { icon: Mail, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "SendGrid" },
  amplitude: { icon: Activity, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "Amplitude" },
  segment: { icon: Share2, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "Segment" },
  pipedrive: { icon: Handshake, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "Pipedrive" },
  freshdesk: { icon: Headphones, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10", label: "Freshdesk" },
  github: { icon: Globe2, color: "text-foreground", bg: "bg-muted", label: "GitHub" },
  openapi: { icon: Globe2, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "OpenAPI / Swagger" },
}

const brandMeta: Record<string, SourceIconMeta> = {
  bigquery: { brand: siGooglebigquery, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "BigQuery" },
  cassandra: { brand: siApachecassandra, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Cassandra" },
  postgres: { brand: siPostgresql, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "PostgreSQL" },
  mysql: { brand: siMysql, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "MySQL" },
  mariadb: { brand: siMariadb, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "MariaDB" },
  singlestore: { brand: siSinglestore, color: "text-fuchsia-600 dark:text-fuchsia-400", bg: "bg-fuchsia-500/10", label: "SingleStore" },
  clickhouse: { brand: siClickhouse, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "ClickHouse" },
  trino: { brand: siTrino, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Trino" },
  presto: { brand: siPresto, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "Presto" },
  snowflake: { brand: siSnowflake, color: "text-cyan-600 dark:text-cyan-400", bg: "bg-cyan-500/10", label: "Snowflake" },
  databricks: { brand: siDatabricks, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Databricks" },
  mongodb: { brand: siMongodb, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "MongoDB" },
  redis: { brand: siRedis, color: "text-red-600 dark:text-red-400", bg: "bg-red-500/10", label: "Redis" },
  elasticsearch: { brand: siElasticsearch, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "Elasticsearch" },
  opensearch: { brand: siOpensearch, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "OpenSearch" },
  influxdb: { brand: siInfluxdb, color: "text-pink-600 dark:text-pink-400", bg: "bg-pink-500/10", label: "InfluxDB" },
  greptimedb: { brand: siGreptimedb, color: "text-lime-600 dark:text-lime-400", bg: "bg-lime-500/10", label: "GreptimeDB" },
  cockroachdb: { brand: siCockroachlabs, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "CockroachDB" },
  timescaledb: { brand: siTimescale, color: "text-amber-600 dark:text-amber-400", bg: "bg-amber-500/10", label: "TimescaleDB" },
  timescale: { brand: siTimescale, color: "text-amber-600 dark:text-amber-400", bg: "bg-amber-500/10", label: "Timescale" },
  aws: { fa: faAws, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "AWS" },
  azure: { fa: faMicrosoft, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Microsoft Azure" },
  sqlserver: { fa: faMicrosoft, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Microsoft SQL Server" },
  "google-cloud": { brand: siGooglecloud, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Google Cloud" },
  neon: { brand: siNeon, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "Neon" },
  supabase: { brand: siSupabase, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "Supabase" },
  planetscale: { brand: siPlanetscale, color: "text-foreground", bg: "bg-muted", label: "PlanetScale" },
  vitess: { brand: siVitess, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "Vitess" },
  "google-sheets": { brand: siGooglesheets, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "Google Sheets" },
  github: { brand: siGithub, color: "text-foreground", bg: "bg-muted", label: "GitHub" },
  gitlab: { brand: siGitlab, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "GitLab" },
  jira: { brand: siJira, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Jira" },
  slack: { fa: faSlack, color: "text-fuchsia-600 dark:text-fuchsia-400", bg: "bg-fuchsia-500/10", label: "Slack" },
  stripe: { brand: siStripe, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "Stripe" },
  notion: { brand: siNotion, color: "text-foreground", bg: "bg-muted", label: "Notion" },
  airtable: { brand: siAirtable, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "Airtable" },
  salesforce: { fa: faSalesforce, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "Salesforce" },
  hubspot: { brand: siHubspot, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "HubSpot" },
  shopify: { brand: siShopify, color: "text-green-600 dark:text-green-400", bg: "bg-green-500/10", label: "Shopify" },
  zendesk: { brand: siZendesk, color: "text-teal-600 dark:text-teal-400", bg: "bg-teal-500/10", label: "Zendesk" },
  intercom: { brand: siIntercom, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Intercom" },
  sentry: { brand: siSentry, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "Sentry" },
  datadog: { brand: siDatadog, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "Datadog" },
  pagerduty: { brand: siPagerduty, color: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10", label: "PagerDuty" },
  "google-analytics": { brand: siGoogleanalytics, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "Google Analytics" },
  mixpanel: { brand: siMixpanel, color: "text-purple-600 dark:text-purple-400", bg: "bg-purple-500/10", label: "Mixpanel" },
  mailchimp: { brand: siMailchimp, color: "text-yellow-600 dark:text-yellow-400", bg: "bg-yellow-500/10", label: "Mailchimp" },
  asana: { brand: siAsana, color: "text-rose-600 dark:text-rose-400", bg: "bg-rose-500/10", label: "Asana" },
  trello: { brand: siTrello, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Trello" },
  clickup: { brand: siClickup, color: "text-violet-600 dark:text-violet-400", bg: "bg-violet-500/10", label: "ClickUp" },
  linear: { brand: siLinear, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "Linear" },
  discord: { brand: siDiscord, color: "text-indigo-600 dark:text-indigo-400", bg: "bg-indigo-500/10", label: "Discord" },
  telegram: { brand: siTelegram, color: "text-sky-600 dark:text-sky-400", bg: "bg-sky-500/10", label: "Telegram" },
  twitter: { brand: siX, color: "text-foreground", bg: "bg-muted", label: "X" },
  neo4j: { brand: siNeo4j, color: "text-blue-600 dark:text-blue-400", bg: "bg-blue-500/10", label: "Neo4j" },
  solr: { brand: siApachesolr, color: "text-orange-600 dark:text-orange-400", bg: "bg-orange-500/10", label: "Solr" },
}

export function sourceMeta(type: string, icon?: string) {
  const keys = [icon, type].filter(Boolean) as string[]
  for (const key of keys) {
    const meta = brandMeta[key] ?? typeMeta[key]
    if (meta) return meta
  }
  return { icon: Database, color: "text-muted-foreground", bg: "bg-muted", label: type } as SourceIconMeta
}

function BrandGlyph({ meta, className }: { meta: SourceIconMeta; className: string }) {
  if (meta.brand) {
    return <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden><path d={meta.brand.path} /></svg>
  }
  if (meta.fa) {
    const [width, height, , , path] = meta.fa.icon
    return <svg viewBox={`0 0 ${width} ${height}`} className={className} fill="currentColor" aria-hidden><path d={Array.isArray(path) ? path.join(" ") : path} /></svg>
  }
  const Icon = meta.icon ?? Database
  return <Icon className={className} />
}

export function SourceIcon({ type, icon, className, size = "md" }: { type: string; icon?: string; className?: string; size?: "sm" | "md" | "lg" }) {
  const meta = sourceMeta(type, icon)
  return (
    <div
      className={cn(
        "grid shrink-0 place-items-center rounded-lg border border-transparent",
        meta.bg,
        size === "sm" && "size-7 rounded-md",
        size === "md" && "size-9",
        size === "lg" && "size-11 rounded-xl",
        className,
      )}
    >
      <BrandGlyph meta={meta} className={cn(meta.color, size === "sm" ? "size-3.5" : size === "md" ? "size-4" : "size-5")} />
    </div>
  )
}

