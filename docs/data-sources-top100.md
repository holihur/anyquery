# Top 100 数据源目录

AnyQuery 当前注册 **102** 个数据源类型，覆盖 SQL、NoSQL、搜索、时序、REST/SaaS API 与文件。

| 成熟度 | 数量 | 含义 |
| --- | ---: | --- |
| `native` | 33 | 独立连接器实现（含 BigQuery REST、Cassandra CQL、MongoDB、Redis、Excel/Access 等） |
| `compatible` | 37 | 复用现有驱动的兼容类型，例如 Aurora、Cloud SQL、Neon、TiDB、Starburst |
| `template` | 32 | 基于通用 REST/CSV/JSONL 的配置模板；仍需填写真实 endpoint、认证和 records path |

> `template` 不等于开箱即用。SaaS API 的 OAuth、分页和字段结构差异很大；模板负责省去重复配置，文档会逐步补充每个服务的 endpoint 示例。


## SQL / 数据仓库（46）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `aurora-mysql` | compatible | Amazon Aurora MySQL |
| `aurora-postgresql` | compatible | Amazon Aurora PostgreSQL |
| `rds-mysql` | compatible | Amazon RDS for MySQL |
| `rds-oracle` | compatible | Amazon RDS for Oracle |
| `rds-postgresql` | compatible | Amazon RDS for PostgreSQL |
| `rds-sqlserver` | compatible | Amazon RDS for SQL Server |
| `redshift` | native | Amazon Redshift |
| `redshift-serverless` | compatible | Amazon Redshift Serverless |
| `azure-mysql` | compatible | Azure Database for MySQL |
| `azure-postgresql` | compatible | Azure Database for PostgreSQL |
| `azure-sql` | compatible | Azure SQL Database |
| `azure-synapse` | compatible | Azure Synapse Analytics |
| `cockroachdb` | native | CockroachDB |
| `cockroach-cloud` | compatible | CockroachDB Cloud |
| `databricks` | native | Databricks SQL |
| `alloydb` | compatible | Google AlloyDB |
| `bigquery` | native | Google BigQuery |
| `cloud-sql-mysql` | compatible | Google Cloud SQL for MySQL |
| `cloud-sql-postgresql` | compatible | Google Cloud SQL for PostgreSQL |
| `cloud-sql-sqlserver` | compatible | Google Cloud SQL for SQL Server |
| `greenplum` | compatible | Greenplum |
| `mariadb` | native | MariaDB |
| `skysql` | compatible | MariaDB SkySQL |
| `materialize` | compatible | Materialize |
| `sqlserver` | native | Microsoft SQL Server |
| `mysql` | native | MySQL |
| `neon` | compatible | Neon |
| `oracle-adb` | compatible | Oracle Autonomous Database |
| `oracle` | native | Oracle Database |
| `percona` | compatible | Percona Server |
| `planetscale` | compatible | PlanetScale |
| `postgres` | native | PostgreSQL |
| `presto` | native | Presto |
| `presto-cloud` | compatible | Presto Cloud |
| `singlestore` | native | SingleStore |
| `singlestore-cloud` | compatible | SingleStore Cloud |
| `snowflake` | native | Snowflake |
| `sqlite` | native | SQLite |
| `starburst` | compatible | Starburst Galaxy |
| `supabase` | compatible | Supabase |
| `tidb` | compatible | TiDB |
| `trino` | native | Trino |
| `vertica` | compatible | Vertica |
| `vitess` | compatible | Vitess |
| `yugabytedb` | native | YugabyteDB |
| `yugabyte-cloud` | compatible | YugabyteDB Managed |

## NoSQL（3）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `cassandra` | native | Apache Cassandra / ScyllaDB |
| `mongodb` | native | MongoDB |
| `redis` | native | Redis |

## 搜索（3）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `sls` | native | Alibaba Cloud SLS |
| `elasticsearch` | native | Elasticsearch |
| `opensearch` | native | OpenSearch |

## 时序 / 日志（11）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `druid` | native | Apache Druid |
| `clickhouse` | native | ClickHouse |
| `clickhouse-cloud` | compatible | ClickHouse Cloud |
| `greptime-cloud` | compatible | GreptimeCloud |
| `greptimedb` | native | GreptimeDB |
| `influxdb` | native | InfluxDB |
| `questdb` | native | QuestDB |
| `questdb-cloud` | compatible | QuestDB Cloud |
| `tdengine` | native | TDengine |
| `timescale-cloud` | compatible | Timescale Cloud |
| `timescaledb` | native | TimescaleDB |

## REST / SaaS API（32）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `airtable` | template | Airtable |
| `amplitude` | template | Amplitude |
| `asana` | template | Asana |
| `clickup` | template | ClickUp |
| `datadog` | template | Datadog |
| `discord` | template | Discord |
| `freshdesk` | template | Freshdesk |
| `github` | template | GitHub |
| `gitlab` | template | GitLab |
| `google-analytics` | template | Google Analytics |
| `hubspot` | template | HubSpot |
| `intercom` | template | Intercom |
| `jira` | template | Jira |
| `linear` | template | Linear |
| `mailchimp` | template | Mailchimp |
| `mixpanel` | template | Mixpanel |
| `notion` | template | Notion |
| `pagerduty` | template | PagerDuty |
| `pipedrive` | template | Pipedrive |
| `rest` | native | REST / JSON API |
| `salesforce` | template | Salesforce |
| `segment` | template | Segment |
| `sendgrid` | template | SendGrid |
| `sentry` | template | Sentry |
| `shopify` | template | Shopify |
| `slack` | template | Slack |
| `stripe` | template | Stripe |
| `telegram` | template | Telegram |
| `trello` | template | Trello |
| `twilio` | template | Twilio |
| `twitter` | template | X / Twitter |
| `zendesk` | template | Zendesk |

## 文件（7）

| 类型 | 成熟度 | 名称 |
| --- | --- | --- |
| `csv` | native | CSV / URL |
| `google-sheets` | template | Google Sheets |
| `jsonl` | native | JSON Lines / URL |
| `access` | native | Microsoft Access |
| `excel` | native | Microsoft Excel |
| `ndjson` | compatible | NDJSON / URL |
| `tsv` | compatible | TSV / URL |
