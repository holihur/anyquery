# 数据源支持进度

> 版本：MVP · 2026-11 · 共 **23** 个内置连接器

## 状态图例

| 标记 | 含义 |
| --- | --- |
| ✅ | 已端到端验证（真实数据源或 CI 集成） |
| 🧪 | 已实现，CI 集成测试覆盖 |
| 🟡 | 已实现，共享代码路径已测试，但尚未用真实账号验证 |
| ⚪ | 规划中 |

> CI 状态：GitHub Actions 已通过 backend/frontend/Docker 构建，以及 MongoDB + SQLite + Redis + JSONL + PostgreSQL 的端到端查询与跨源 JOIN 测试。
>
> 当前 MVP 的联邦策略：连接器负责 **连接 / Schema 探测 / 有界取数**，跨源 JOIN、过滤、聚合、排序由 Go 查询引擎完成。已支持 LIMIT 下推；**谓词/投影下推仍在 roadmap**，所以表格中的“查询下推”统一为 LIMIT。

## 总览

| # | 数据源 | 类型 | 状态 | Schema 探测 | 认证 | 过滤下推 | 验证方式 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | PostgreSQL | SQL | ✅ | `information_schema` | 用户名/密码、SSL、DSN | LIMIT | 本地 PostgreSQL 16 + CI 跨源 JOIN |
| 2 | CockroachDB | SQL | 🟡 | `information_schema` | 用户名/密码、SSL | LIMIT | 编译 + 共享 SQL 测试 |
| 3 | Amazon Redshift | SQL | 🟡 | `information_schema` | 用户名/密码、SSL | LIMIT | 编译 + 共享 SQL 测试 |
| 4 | TimescaleDB | SQL | 🟡 | `information_schema` | 用户名/密码、SSL | LIMIT | 编译 + 共享 SQL 测试 |
| 5 | YugabyteDB | SQL | 🟡 | `information_schema` | 用户名/密码、SSL | LIMIT | 编译 + 共享 SQL 测试 |
| 6 | MySQL | SQL | 🟡 | `information_schema` | 用户名/密码、TLS、DSN | LIMIT | 编译 + 共享 SQL 测试 |
| 7 | MariaDB | SQL | 🟡 | `information_schema` | 用户名/密码、TLS | LIMIT | 编译 + 共享 SQL 测试 |
| 8 | SingleStore | SQL | 🟡 | `information_schema` | 用户名/密码、TLS | LIMIT | 编译 + 共享 SQL 测试 |
| 9 | Microsoft SQL Server | SQL | 🟡 | `information_schema` | SQL 认证、Encrypt | TOP | 编译 + 共享 SQL 测试 |
| 10 | SQLite | SQL | ✅ | `sqlite_master` + `PRAGMA` | 文件路径 | LIMIT | 演示工作区 + CI |
| 11 | ClickHouse | SQL | 🟡 | `system.tables/columns` | 用户名/密码、TLS | LIMIT | 编译 + 共享 SQL 测试 |
| 12 | Trino | SQL | 🟡 | `information_schema` | Basic/无密码、HTTPS | LIMIT | 编译 + 共享 SQL 测试 |
| 13 | Presto | SQL | 🟡 | `information_schema` | Basic/无密码、HTTPS | LIMIT | 编译 + 共享 SQL 测试 |
| 14 | Oracle | SQL | 🟡 | `all_tables` / `all_tab_columns` | 用户名/密码、service name | FETCH FIRST | 编译 + 共享 SQL 测试 |
| 15 | Snowflake | 云数仓 | 🟡 | `information_schema` | OAuth / Programmatic Access Token | LIMIT | 编译 + HTTP API 单测（无真实账号） |
| 16 | Databricks SQL | 云数仓 | 🟡 | `information_schema` | Personal Access Token | LIMIT | 编译 + HTTP API 单测（无真实账号） |
| 17 | MongoDB | NoSQL | ✅ | 采样文档推断字段 | URI（SCRAM/TLS） | LIMIT | 演示工作区 + CI |
| 18 | Redis | NoSQL | ✅ | SCAN 采样 + Hash/JSON 展开 | URL（ACL、TLS） | SCAN + 行数上限 | CI（Redis 7）+ Mongo JOIN |
| 19 | Elasticsearch | Search | 🟡 | `_mapping` | Basic / ApiKey | size | 编译 + HTTP 路径测试 |
| 20 | OpenSearch | Search | 🟡 | `_mapping` | Basic / ApiKey | size | 编译 + HTTP 路径测试 |
| 21 | REST / JSON | API | ✅ | 响应采样推断 | Header（Bearer 等） | 客户端行数上限 | 本地端到端 + CI |
| 22 | CSV / TSV URL | File | ✅ | Header + 类型推断 | Header | 下载后限制 | 本地端到端 |
| 23 | JSON Lines / NDJSON | File | ✅ | 逐行采样推断 | Header | 逐行读取限制 | CI + Mongo JOIN |

## 分项说明

### SQL 家族（1–14）

共享 `internal/sqlconn` 实现，因此行为高度一致：

- **连接**：标准 `database/sql`。支持字段式连接（host/port/database/user/password/SSL）或高级 DSN。
- **Schema**：默认只暴露当前 schema/database 的表，避免一次拉取整实例；表名以 `schema.table` 形式暴露。
- **查询**：`SELECT *` + 方言 LIMIT（SQL Server 使用 `TOP`，Oracle 使用 `FETCH FIRST`），然后由引擎完成过滤/JOIN/聚合。
- **标识符**：按方言安全引用（`"` / `` ` `` / `[]`）。
- **注意**：
  - 未实现谓词下推，大表请使用 LIMIT/过滤条件或专用视图。
  - Trino/Presto 的 catalog/schema 在连接配置中指定；跨 catalog 查询需要显式配置或 DSN。
  - Snowflake/Databricks 走 HTTP API，不使用 Arrow/AWS SDK，因此结果集会受服务端 inline/partition 限制。
  - 建议使用只读账号；平台不会执行源端 DDL。

| 数据源 | 驱动/协议 | 默认端口 | 备注 |
| --- | --- | --- | --- |
| PostgreSQL | `pgx` | 5432 | 支持 Aurora PostgreSQL |
| CockroachDB | PostgreSQL wire | 26257 | 排除 `crdb_internal` |
| Redshift | PostgreSQL wire | 5439 | |
| TimescaleDB | PostgreSQL wire | 5432 | 排除 `_timescaledb_internal` |
| YugabyteDB | PostgreSQL wire | 5433 | |
| MySQL | `go-sql-driver/mysql` | 3306 | Aurora MySQL 可用 |
| MariaDB | MySQL wire | 3306 | |
| SingleStore | MySQL wire | 3306 | |
| SQL Server | `go-mssqldb` | 1433 | Azure SQL 可用 |
| SQLite | `modernc.org/sqlite`（纯 Go） | — | 适合本地/演示/单文件分析 |
| ClickHouse | `clickhouse-go/v2` | 9000 | Native 协议 |
| Trino | `trino-go-client` | 8080 | |
| Presto | Trino wire | 8080 | PrestoDB 兼容性取决于版本 |
| Oracle | `go-ora` | 1521 | service name；需 Oracle 账户 |

### Snowflake / Databricks（15–16）

- 使用官方 **HTTP SQL API**，避免 Snowflake/Databricks 官方 Go 驱动引入的 Arrow/AWS SDK 巨型依赖。
- Snowflake：`/api/v2/statements`，支持 OAuth / PAT；异步轮询；当前取首个 partition（超大数据集请加过滤/LIMIT）。
- Databricks：`/api/2.0/sql/statements/`，PAT + SQL warehouse ID；inline JSON_ARRAY 结果。
- 两者都需要显式配置 warehouse/warehouse_id；未验证真实企业账号（状态 🟡）。

### MongoDB（17）

- **连接**：标准 URI；建议只读用户。
- **Schema**：对每个 collection 使用 `$sample` 采样（默认 30 条）推断字段和类型，避免全表扫描。
- **行值**：ObjectID → hex 字符串；DateTime → RFC3339；Decimal128 → 字符串；嵌套对象/数组保留为 JSON 值。
- **限制**：只展开顶层字段；Schema 采样可能漏掉稀有字段（引擎在运行时仍会保留新字段）。无谓词下推。
- **已验证**：演示数据（customers/orders）+ CI 跨源 JOIN。

### Redis（18）

Redis 不是关系型数据库，AnyQuery 将匹配 `key_pattern` 的 key 映射为一行：

| 保留列 | 类型 | 说明 |
| --- | --- | --- |
| `_key` | string | 完整 key |
| `_type` | string | `string` / `hash` / `list` / `set` / `zset` / `stream` |
| `_ttl` | integer | 剩余秒数；`-1` 表示无过期 |
| `_size` | integer | 字符串长度或集合大小 |
| `_value` | string | 原始值或 JSON 表示 |

- Hash 字段和 JSON 对象的顶层字段会成为普通列，因此可以直接跨源 JOIN。
- 集合类型最多读取 1000 个元素到 `_value`，`_size` 仍是完整大小。
- `key_pattern` 使用 SCAN glob（如 `user:*`）；`scan_limit` 默认 5000，最大 50000。
- 只执行只读命令；建议 ACL 只授予 `SCAN/GET/HGETALL/…` 权限。
- 已验证：CI 使用 Redis 7 写入 hash，再执行查询与 JOIN。

### Elasticsearch / OpenSearch（19–20）

- 必须指定 index 或 index pattern；通过 `_mapping` 推断字段类型。
- 查询使用 `_search` + `match_all`，默认最多 10,000 行（ES 默认窗口）。
- 嵌套对象/数组会序列化为 JSON 字符串，便于展示但不利于继续展开。
- 认证：Basic 或 API Key；可选跳过 TLS 校验（不建议生产使用）。
- 尚未使用真实集群验证（🟡）。

### REST / JSON（21）

- GET 一个 JSON endpoint；根数组或自动识别 `data/items/records/results/rows/values`，也可配置 `records_path`。
- Header 使用 JSON 配置（Secret 加密），适合 Bearer Token。
- 嵌套对象会序列化为 JSON 字符串。
- **无分页协议**：只读取单次响应，最多客户端截断；大 API 建议在网关侧分页或使用专用连接器。
- 安全问题：URL 由工作区 admin 配置；生产环境请配合出口网络策略防 SSRF。

### CSV / TSV（22）

- 自动读取 header，支持逗号/分号/Tab/竖线。
- 数值、布尔值会做类型推断，便于聚合/排序。
- 整文件下载后解析；大文件会占用内存。支持自定义 HTTP headers。
- 不支持远端分页或范围读取。

### JSON Lines / NDJSON（23）

- 每行一个 JSON value；对象会展开顶层字段，标量放入 `value` 列。
- 通过 `bufio.Scanner` 流式读取，单行上限 16MB，最多读取服务端行数护栏。
- 适合事件日志、导出文件、LLM 数据集等。
- 支持自定义 HTTP headers；无分页。
- CI 使用本地 HTTP 文件验证连接、Schema 探测与查询。

## 跨数据源 JOIN 支持

所有连接器共用同一套执行引擎，因此任意两个（甚至多个）数据源都可以 JOIN：

```sql
SELECT c.name, p.category, sum(o.total) AS revenue
FROM mongo.orders o
JOIN mongo.customers c ON o.customer_id = c.id
JOIN shop.products p ON o.product_id = p.id
JOIN redis.sessions s ON s.user_id = c.id
WHERE o.status = 'paid'
GROUP BY c.name, p.category
```

能力矩阵：

| 能力 | 状态 |
| --- | --- |
| INNER / LEFT / RIGHT / FULL / CROSS JOIN | ✅ |
| 等值 JOIN 使用 Hash Join（跨源数值类型归一化） | ✅ |
| 复合 JOIN key | ✅ |
| JOIN 上附加过滤条件 | ✅（Hash 候选后再评估） |
| OR / 非等值条件 | ✅（回退 Nested Loop） |
| 3+ 数据源串联 JOIN | ✅ |
| JOIN 中间结果上限 | ✅（默认 2,000,000 行） |
| 跨源谓词下推 | ⚪ Roadmap |
| 跨源并发扫描 | ⚪ Roadmap |

## 通用限制

- 每个源最多拉取 `QUERY_MAX_SOURCE_ROWS`（默认 100,000）行；结果最多 `QUERY_MAX_RESULT_ROWS`（默认 10,000）行。
- 每个 workspace 最多保存 100 条查询；查询历史展示最近 100 条。
- 查询超时默认 30 秒；JOIN 中间结果超过上限会报错。
- Truncated 结果会在 UI 与响应 warnings 中提示，不会静默丢弃。
- 所有凭据 AES-256-GCM 加密；连接器只返回脱敏配置。
- 平台不执行写入语句；连接器仅实现读取。

## 下一步

1. 增加谓词/投影下推和各连接器的原生分页。
2. 为 MySQL/Postgres/SQL Server/ClickHouse 建 CI 容器集成测试。
3. MongoDB 采样 Schema 增加 nested path 展开选项。
4. Redis 增加 key-pattern 多表映射（按前缀自动分表）与 RedisJSON 支持。
5. Elasticsearch 使用 PIT + 分页读取超过 10k 行。
6. REST 连接器增加分页模板（cursor/page/offset）。
7. Snowflake 多 partition 完整读取、Databricks 外部 links 分块读取。
8. 数据源健康检查定时任务与最近失败原因展示。

如果你验证了某个 🟡 数据源，欢迎提交 PR 更新此表并附上连接方式（不含凭据）、Schema 截图和查询样例。
