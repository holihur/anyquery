// Package presets registers first-class data source types that reuse an
// existing connector implementation. SQL presets are wire-compatible services;
// API presets are configurable REST templates. The maturity field tells the UI
// how much is guaranteed out of the box.
package presets

import (
	"github.com/anyquery/anyquery/internal/connector"
)

type sqlPreset struct {
	Base, Type, Name, Description, Icon, Port string
}

var sqlPresets = []sqlPreset{
	{"postgres", "aurora-postgresql", "Amazon Aurora PostgreSQL", "Aurora PostgreSQL (PostgreSQL wire compatible)", "postgres", "5432"},
	{"mysql", "aurora-mysql", "Amazon Aurora MySQL", "Aurora MySQL (MySQL wire compatible)", "mysql", "3306"},
	{"postgres", "azure-postgresql", "Azure Database for PostgreSQL", "Azure managed PostgreSQL", "postgres", "5432"},
	{"mysql", "azure-mysql", "Azure Database for MySQL", "Azure managed MySQL", "mysql", "3306"},
	{"sqlserver", "azure-sql", "Azure SQL Database", "Azure SQL Database (T-SQL)", "sqlserver", "1433"},
	{"sqlserver", "azure-synapse", "Azure Synapse Analytics", "Azure Synapse dedicated SQL pool (T-SQL)", "sqlserver", "1433"},
	{"postgres", "cloud-sql-postgresql", "Google Cloud SQL for PostgreSQL", "Cloud SQL PostgreSQL", "postgres", "5432"},
	{"mysql", "cloud-sql-mysql", "Google Cloud SQL for MySQL", "Cloud SQL MySQL", "mysql", "3306"},
	{"sqlserver", "cloud-sql-sqlserver", "Google Cloud SQL for SQL Server", "Cloud SQL SQL Server", "sqlserver", "1433"},
	{"postgres", "neon", "Neon", "Serverless PostgreSQL", "postgres", "5432"},
	{"postgres", "supabase", "Supabase", "Supabase PostgreSQL", "postgres", "5432"},
	{"postgres", "alloydb", "Google AlloyDB", "AlloyDB for PostgreSQL", "postgres", "5432"},
	{"postgres", "greenplum", "Greenplum", "Greenplum / Cloudberry (PostgreSQL wire)", "postgres", "5432"},
	{"postgres", "rds-postgresql", "Amazon RDS for PostgreSQL", "RDS PostgreSQL", "postgres", "5432"},
	{"mysql", "rds-mysql", "Amazon RDS for MySQL", "RDS MySQL", "mysql", "3306"},
	{"sqlserver", "rds-sqlserver", "Amazon RDS for SQL Server", "RDS SQL Server", "sqlserver", "1433"},
	{"oracle", "rds-oracle", "Amazon RDS for Oracle", "RDS Oracle", "oracle", "1521"},
	{"oracle", "oracle-adb", "Oracle Autonomous Database", "Oracle Autonomous Database", "oracle", "1521"},
	{"mysql", "tidb", "TiDB", "TiDB Cloud / self-hosted (MySQL wire)", "mysql", "4000"},
	{"mysql", "planetscale", "PlanetScale", "PlanetScale MySQL-compatible database", "mysql", "3306"},
	{"mysql", "vitess", "Vitess", "Vitess MySQL-compatible cluster", "mysql", "3306"},
	{"mysql", "percona", "Percona Server", "Percona MySQL-compatible server", "mysql", "3306"},
	{"mysql", "skysql", "MariaDB SkySQL", "SkySQL managed MariaDB", "mariadb", "3306"},
	{"mysql", "singlestore-cloud", "SingleStore Cloud", "SingleStore managed service", "singlestore", "3306"},
	{"timescaledb", "timescale-cloud", "Timescale Cloud", "Managed TimescaleDB", "timescaledb", "5432"},
	{"yugabytedb", "yugabyte-cloud", "YugabyteDB Managed", "Managed YugabyteDB", "yugabytedb", "5433"},
	{"cockroachdb", "cockroach-cloud", "CockroachDB Cloud", "Managed CockroachDB", "cockroachdb", "26257"},
	{"redshift", "redshift-serverless", "Amazon Redshift Serverless", "Redshift Serverless", "redshift", "5439"},
	{"questdb", "questdb-cloud", "QuestDB Cloud", "Managed QuestDB", "questdb", "8812"},
	{"greptimedb", "greptime-cloud", "GreptimeCloud", "Managed GreptimeDB", "greptimedb", "4003"},
	{"clickhouse", "clickhouse-cloud", "ClickHouse Cloud", "Managed ClickHouse", "clickhouse", "9000"},
	{"trino", "starburst", "Starburst Galaxy", "Managed Trino", "trino", "8080"},
	{"presto", "presto-cloud", "Presto Cloud", "Managed Presto", "presto", "8080"},
	{"postgres", "materialize", "Materialize", "Streaming SQL, PostgreSQL wire compatible", "postgres", "6875"},
	{"postgres", "vertica", "Vertica", "Vertica (PostgreSQL-compatible mode)", "postgres", "5433"},
}

type restPreset struct {
	Type, Name, Description, Icon, RecordsPath, TableName, URLHelp string
}

var restPresets = []restPreset{
	{"github", "GitHub", "GitHub REST API", "github", "items", "issues", "https://api.github.com/repos/{owner}/{repo}/issues"},
	{"gitlab", "GitLab", "GitLab REST API", "gitlab", "", "issues", "https://gitlab.com/api/v4/projects/{id}/issues"},
	{"jira", "Jira", "Jira REST API", "jira", "issues", "issues", "https://{site}.atlassian.net/rest/api/3/search"},
	{"slack", "Slack", "Slack Web API", "slack", "members", "members", "https://slack.com/api/users.list"},
	{"stripe", "Stripe", "Stripe REST API", "stripe", "data", "charges", "https://api.stripe.com/v1/charges"},
	{"notion", "Notion", "Notion API", "notion", "results", "pages", "https://api.notion.com/v1/search"},
	{"airtable", "Airtable", "Airtable REST API", "airtable", "records", "records", "https://api.airtable.com/v0/{baseId}/{tableId}"},
	{"salesforce", "Salesforce", "Salesforce REST API", "salesforce", "records", "records", "https://{instance}.salesforce.com/services/data/v60.0/query?q=..."},
	{"hubspot", "HubSpot", "HubSpot CRM API", "hubspot", "results", "contacts", "https://api.hubapi.com/crm/v3/objects/contacts"},
	{"shopify", "Shopify", "Shopify Admin API", "shopify", "orders", "orders", "https://{shop}.myshopify.com/admin/api/2024-10/orders.json"},
	{"zendesk", "Zendesk", "Zendesk Support API", "zendesk", "tickets", "tickets", "https://{subdomain}.zendesk.com/api/v2/tickets.json"},
	{"intercom", "Intercom", "Intercom REST API", "intercom", "data", "contacts", "https://api.intercom.io/contacts"},
	{"sentry", "Sentry", "Sentry API", "sentry", "", "issues", "https://sentry.io/api/0/projects/{org}/{project}/issues/"},
	{"datadog", "Datadog", "Datadog API", "datadog", "series", "metrics", "https://api.datadoghq.com/api/v1/query?query=..."},
	{"pagerduty", "PagerDuty", "PagerDuty API", "pagerduty", "incidents", "incidents", "https://api.pagerduty.com/incidents"},
	{"twilio", "Twilio", "Twilio API", "twilio", "messages", "messages", "https://api.twilio.com/2010-04-01/Accounts/{sid}/Messages.json"},
	{"sendgrid", "SendGrid", "SendGrid API", "sendgrid", "", "stats", "https://api.sendgrid.com/v3/stats?start_date=..."},
	{"google-analytics", "Google Analytics", "Google Analytics Data API", "google", "rows", "metrics", "https://analyticsdata.googleapis.com/v1beta/properties/{id}:runReport"},
	{"mixpanel", "Mixpanel", "Mixpanel Export API", "mixpanel", "results", "events", "https://data.mixpanel.com/api/2.0/export?from_date=...&to_date=..."},
	{"amplitude", "Amplitude", "Amplitude Dashboard API", "amplitude", "data", "events", "https://amplitude.com/api/2/events/segmentation?e=...&start=...&end=..."},
	{"segment", "Segment", "Segment Profiles API", "segment", "data", "profiles", "https://api.segment.io/v1/..."},
	{"mailchimp", "Mailchimp", "Mailchimp Marketing API", "mailchimp", "members", "members", "https://{dc}.api.mailchimp.com/3.0/lists/{listId}/members"},
	{"asana", "Asana", "Asana API", "asana", "data", "tasks", "https://app.asana.com/api/1.0/tasks"},
	{"trello", "Trello", "Trello REST API", "trello", "", "cards", "https://api.trello.com/1/boards/{id}/cards"},
	{"clickup", "ClickUp", "ClickUp API", "clickup", "tasks", "tasks", "https://api.clickup.com/api/v2/list/{id}/task"},
	{"pipedrive", "Pipedrive", "Pipedrive API", "pipedrive", "data", "deals", "https://api.pipedrive.com/v1/deals"},
	{"freshdesk", "Freshdesk", "Freshdesk API", "freshdesk", "", "tickets", "https://{domain}.freshdesk.com/api/v2/tickets"},
	{"discord", "Discord", "Discord API", "discord", "", "messages", "https://discord.com/api/v10/channels/{id}/messages"},
	{"telegram", "Telegram", "Telegram Bot API", "telegram", "result", "updates", "https://api.telegram.org/bot{token}/getUpdates"},
	{"twitter", "X / Twitter", "X API v2", "twitter", "data", "posts", "https://api.x.com/2/tweets/search/recent?query=..."},
}

type restRecipe struct {
	Method        string
	Body          string
	Pagination    string
	PageParam     string
	PageSizeParam string
	OffsetParam   string
	CursorParam   string
	CursorPath    string
	NextPath      string
}

// Recipes are best-effort defaults for popular APIs. They remain templates
// until a connector has been verified against a real endpoint, but they save
// users from guessing pagination and auth field names.
var restRecipes = map[string]restRecipe{
	"github":     {Pagination: "link", PageSizeParam: "per_page"},
	"gitlab":     {Pagination: "page", PageParam: "page", PageSizeParam: "per_page"},
	"jira":       {Pagination: "offset", OffsetParam: "startAt", PageSizeParam: "maxResults"},
	"slack":      {Pagination: "cursor", CursorParam: "cursor", CursorPath: "response_metadata.next_cursor", PageSizeParam: "limit"},
	"notion":     {Method: "POST", Body: `{"query":""}`},
	"airtable":   {Pagination: "cursor", CursorParam: "offset", CursorPath: "offset"},
	"salesforce": {Pagination: "link", NextPath: "nextRecordsUrl"},
	"hubspot":    {Pagination: "cursor", CursorParam: "after", CursorPath: "paging.next.after", PageSizeParam: "limit"},
	"shopify":    {Pagination: "link", PageSizeParam: "limit"},
	"zendesk":    {Pagination: "link", PageSizeParam: "per_page"},
	"intercom":   {Pagination: "link", NextPath: "pages.next", PageSizeParam: "per_page"},
	"pagerduty":  {Pagination: "offset", OffsetParam: "offset", PageSizeParam: "limit"},
	"asana":      {Pagination: "cursor", CursorParam: "offset", CursorPath: "next_page.offset", PageSizeParam: "limit"},
	"twitter":    {Pagination: "cursor", CursorParam: "pagination_token", CursorPath: "meta.next_token", PageSizeParam: "max_results"},
	"telegram":   {Pagination: "offset", OffsetParam: "offset", PageSizeParam: "limit"},
	"clickup":    {Pagination: "page", PageParam: "page", PageSizeParam: "page_size"},
}

type graphqlPreset struct {
	Type, Name, Description, Icon, Endpoint, Query, DataPath, Columns, TableName string
}

var graphqlPresets = []graphqlPreset{{
	Type: "linear", Name: "Linear", Description: "Linear GraphQL API (template)",
	Icon: "linear", Endpoint: "https://api.linear.app/graphql",
	TableName: "issues", DataPath: "issues.nodes",
	Query: `query($after: String) {
  issues(first: 100, after: $after) {
    nodes { id identifier title priority createdAt state { name } }
    pageInfo { hasNextPage endCursor }
  }
}`,
	Columns: `{"id":"string","identifier":"string","title":"string","priority":"number","createdAt":"string","state":"object"}`,
}}

func registerGraphQLPresets() {
	for _, p := range graphqlPresets {
		base, ok := connector.Get("graphql")
		if !ok {
			continue
		}
		spec := base.Spec()
		spec.Type = p.Type
		spec.Name = p.Name
		spec.Description = p.Description
		spec.Icon = p.Icon
		spec.Category = connector.CategoryAPI
		spec.Maturity = "template"
		defaults := map[string]string{
			"endpoint": p.Endpoint, "table_name": p.TableName, "query": p.Query,
			"data_path": p.DataPath, "columns": p.Columns,
			"cursor_variable": "after", "cursor_path": "issues.pageInfo.endCursor",
			"has_next_path": "issues.pageInfo.hasNextPage", "max_pages": "10",
		}
		for i := range spec.Fields {
			if v, ok := defaults[spec.Fields[i].Key]; ok {
				spec.Fields[i].Default = v
			}
		}
		connector.RegisterAlias("graphql", spec, defaults)
	}
}

func sqlIcon(typ, fallback string) string {
	switch typ {
	case "aurora-postgresql", "aurora-mysql", "rds-postgresql", "rds-mysql", "rds-sqlserver", "rds-oracle", "redshift-serverless":
		return "aws"
	case "azure-postgresql", "azure-mysql", "azure-sql", "azure-synapse":
		return "azure"
	case "cloud-sql-postgresql", "cloud-sql-mysql", "cloud-sql-sqlserver", "alloydb":
		return "google-cloud"
	case "neon":
		return "neon"
	case "supabase":
		return "supabase"
	case "planetscale":
		return "planetscale"
	case "vitess":
		return "vitess"
	case "timescale-cloud":
		return "timescale"
	case "clickhouse-cloud":
		return "clickhouse"
	case "starburst":
		return "trino"
	case "presto-cloud":
		return "presto"
	case "singlestore-cloud":
		return "singlestore"
	case "skysql":
		return "mariadb"
	case "tidb", "percona":
		return "mysql"
	case "materialize":
		return "postgres"
	case "questdb-cloud":
		return "questdb"
	case "greptime-cloud":
		return "greptimedb"
	case "cockroach-cloud":
		return "cockroachdb"
	case "yugabyte-cloud":
		return "yugabytedb"
	case "oracle-adb":
		return "oracle"
	default:
		return fallback
	}
}

func applyRestRecipe(spec *connector.TypeSpec, recipe restRecipe) {
	for i := range spec.Fields {
		switch spec.Fields[i].Key {
		case "http_method":
			if recipe.Method != "" {
				spec.Fields[i].Default = recipe.Method
			}
		case "body_template":
			if recipe.Body != "" {
				spec.Fields[i].Default = recipe.Body
			}
		case "pagination":
			if recipe.Pagination != "" {
				spec.Fields[i].Default = recipe.Pagination
			}
		case "page_param":
			if recipe.PageParam != "" {
				spec.Fields[i].Default = recipe.PageParam
			}
		case "page_size_param":
			if recipe.PageSizeParam != "" {
				spec.Fields[i].Default = recipe.PageSizeParam
			}
		case "offset_param":
			if recipe.OffsetParam != "" {
				spec.Fields[i].Default = recipe.OffsetParam
			}
		case "cursor_param":
			if recipe.CursorParam != "" {
				spec.Fields[i].Default = recipe.CursorParam
			}
		case "cursor_path":
			if recipe.CursorPath != "" {
				spec.Fields[i].Default = recipe.CursorPath
			}
		case "next_path":
			if recipe.NextPath != "" {
				spec.Fields[i].Default = recipe.NextPath
			}
		}
	}
}

func RegisterAll() {
	for _, p := range sqlPresets {
		base, ok := connector.Get(p.Base)
		if !ok {
			continue
		}
		spec := base.Spec()
		spec.Type = p.Type
		spec.Name = p.Name
		spec.Description = p.Description
		spec.Icon = sqlIcon(p.Type, p.Icon)
		spec.Maturity = "compatible"
		if p.Port != "" {
			for i := range spec.Fields {
				if spec.Fields[i].Key == "port" {
					spec.Fields[i].Default = p.Port
				}
			}
		}
		connector.RegisterAlias(p.Base, spec, nil)
	}
	registerGraphQLPresets()
	for _, p := range restPresets {
		base, ok := connector.Get("rest")
		if !ok {
			continue
		}
		spec := base.Spec()
		spec.Type = p.Type
		spec.Name = p.Name
		spec.Description = p.Description + " (REST template)"
		spec.Icon = p.Icon
		spec.Category = connector.CategoryAPI
		spec.Maturity = "template"
		for i := range spec.Fields {
			switch spec.Fields[i].Key {
			case "records_path":
				spec.Fields[i].Default = p.RecordsPath
			case "table_name":
				spec.Fields[i].Default = p.TableName
			case "url":
				spec.Fields[i].Placeholder = "https://api.example.com/v1/records"
				spec.Fields[i].Help = "Example: " + p.URLHelp
			case "headers":
				spec.Fields[i].Help = "Authorization header/API key, for example {\"Authorization\":\"Bearer ...\"}"
			}
		}
		if recipe, ok := restRecipes[p.Type]; ok {
			applyRestRecipe(&spec, recipe)
		}
		connector.RegisterAlias("rest", spec, nil)
	}
	registerFileAliases()
}

func registerFileAliases() {
	if base, ok := connector.Get("csv"); ok {
		spec := base.Spec()
		spec.Type = "tsv"
		spec.Name = "TSV / URL"
		spec.Description = "Remote tab-separated values"
		spec.Icon = "csv"
		spec.Maturity = "compatible"
		for i := range spec.Fields {
			if spec.Fields[i].Key == "delimiter" {
				spec.Fields[i].Default = "\t"
			}
		}
		connector.RegisterAlias("csv", spec, nil)

		sheet := base.Spec()
		sheet.Type = "google-sheets"
		sheet.Name = "Google Sheets"
		sheet.Description = "Published Google Sheet CSV export"
		sheet.Icon = "google-sheets"
		sheet.Maturity = "template"
		for i := range sheet.Fields {
			if sheet.Fields[i].Key == "url" {
				sheet.Fields[i].Label = "Published CSV URL"
				sheet.Fields[i].Placeholder = "https://docs.google.com/spreadsheets/d/.../export?format=csv"
			}
		}
		connector.RegisterAlias("csv", sheet, nil)
	}
	if base, ok := connector.Get("jsonl"); ok {
		spec := base.Spec()
		spec.Type = "ndjson"
		spec.Name = "NDJSON / URL"
		spec.Description = "Remote newline-delimited JSON"
		spec.Icon = "jsonl"
		spec.Maturity = "compatible"
		connector.RegisterAlias("jsonl", spec, nil)
	}
}
