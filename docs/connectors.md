# 添加一个连接器

AnyQuery 的查询引擎只依赖 `internal/connector` 中的抽象，因此接入新系统不需要改 parser、执行器或 API。

## 1. 接口

```go
// internal/connector/connector.go
type Provider interface {
    Spec() TypeSpec
    Open(ctx context.Context, config map[string]string) (Connector, error)
}

type Connector interface {
    Ping(ctx context.Context) error
    Introspect(ctx context.Context) ([]Table, error)
    Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error)
    Capabilities() Capabilities
    Close() error
}
```

核心结构：

```go
type TypeSpec struct {
    Type        string        // 唯一 key，例如 "postgres"
    Name        string        // UI 展示名
    Category    Category      // sql / nosql / search / api / file
    Description string
    Icon        string
    Fields      []ConfigField // 前端动态表单
}

type ConfigField struct {
    Key      string
    Label    string
    Kind     FieldKind // text / password / number / textarea / select / bool
    Required bool
    Secret   bool      // 加密存储，API 返回时脱敏
    // placeholder, help, default, options...
}

type Table struct {
    Name        string   // SQL 中 table 部分；可以包含 schema，如 "public.users"
    DisplayName string
    Schema      string
    Kind        string   // table / view / collection / index / api / file
    Columns     []Column
}

type FetchRequest struct {
    Table   string
    Columns []string
    Limit   int // 必须遵守，防止拉取无界数据
}

type FetchResult struct {
    Columns   []Column
    Rows      []map[string]any // 值需为 JSON-friendly
    Truncated bool
}
```

**值规范**：字符串、布尔、数字、`nil`、`map[string]any`、`[]any`、RFC3339 时间字符串。不要返回 driver 专有类型（如 Mongo `primitive.ObjectID`、`time.Time`、`[]byte`），否则比较/聚合/JSON 序列化会不稳定。

## 2. 最小实现

```go
package mysource

import (
    "context"

    "github.com/anyquery/anyquery/internal/connector"
)

func init() { connector.Register(Provider{}) }

type Provider struct{}

func (Provider) Spec() connector.TypeSpec {
    return connector.TypeSpec{
        Type:     "mysource",
        Name:     "My Source",
        Category: connector.CategoryAPI,
        Fields: []connector.ConfigField{
            {Key: "endpoint", Label: "Endpoint", Kind: connector.FieldText, Required: true},
            {Key: "token", Label: "Token", Kind: connector.FieldPassword, Required: true, Secret: true},
        },
    }
}

func (Provider) Open(ctx context.Context, cfg map[string]string) (connector.Connector, error) {
    c := &Conn{endpoint: cfg["endpoint"], token: cfg["token"]}
    if err := c.Ping(ctx); err != nil {
        return nil, err
    }
    return c, nil
}

type Conn struct {
    endpoint string
    token    string
}

func (c *Conn) Ping(ctx context.Context) error { return nil }

func (c *Conn) Capabilities() connector.Capabilities {
    return connector.Capabilities{LimitPushdown: true}
}

func (c *Conn) Close() error { return nil }

func (c *Conn) Introspect(ctx context.Context) ([]connector.Table, error) {
    // 返回表与列，供 SQL 补全和 catalog 使用。
    return []connector.Table{{
        Name: "records",
        Kind: "api",
        Columns: []connector.Column{
            {Name: "id", Type: "integer"},
            {Name: "name", Type: "string", Nullable: true},
        },
    }}, nil
}

func (c *Conn) Fetch(ctx context.Context, req connector.FetchRequest) (*connector.FetchResult, error) {
    // 1. 校验 req.Table
    // 2. 请求远端，遵守 req.Limit
    // 3. 归一化值
    return &connector.FetchResult{Rows: rows, Columns: cols}, nil
}
```

## 3. 注册

1. 在 `backend/internal/connectors/all/all.go` 中 blank import：

```go
import _ "github.com/anyquery/anyquery/internal/mysource"
```

2. 启动后 `GET /api/v1/datasource-types` 会返回这个类型。
3. 前端会自动生成连接表单，无需改 React 代码。

## 4. 复用现有实现

### SQL 方言

16 个 SQL 数据源共用 `internal/sqlconn`。新增兼容数据库时，在 `Dialects()` 中增加一个 `Dialect`：

- `Driver`：已注册的 `database/sql` driver 名称
- `BuildDSN`：把配置转为 DSN
- `Quote`：标识符引用
- `LimitSQL`：LIMIT 语法（SQL Server 使用 TOP，Oracle 使用 FETCH FIRST）
- `Introspect`：information_schema / system catalog 查询

### HTTP / JSON

参考 `internal/httpjson`：

- REST：`records_path` 从响应中定位数组，自动推断列。
- CSV：解析 header 与类型。
- 两者都支持自定义 headers（Secret）和行数上限。

### Search

参考 `internal/elasticsearch`：mapping → columns，`_search` → rows。

### 云数仓 HTTP API

参考 `internal/cloudsql`（Snowflake SQL API / Databricks Statement Execution API）。这种方式避免引入庞大的 Arrow/AWS SDK 依赖。

## 5. 约定与检查清单

- [ ] `Open` 内验证必填配置并执行 `Ping`，设置合理的超时。
- [ ] `Introspect` 限制返回数量（建议 ≤ 500 表 / 20k 列），跳过系统 schema。
- [ ] `Fetch` 严格遵守 `Limit`，大结果返回 `Truncated: true`。
- [ ] 归一化时间、二进制、ObjectID、Decimal 等类型。
- [ ] 列名保持远端原始大小写；引擎做大小写不敏感解析。
- [ ] 不记录凭据到日志。
- [ ] 对 SQL 源的表名做方言安全引用；不要拼接用户输入。
- [ ] 写单元测试（可以像 `engine_test.go` 一样使用 fake connector）。
- [ ] 在 README 的支持列表中更新。

## 6. 测试连接器

```go
func TestProviderSpec(t *testing.T) {
    spec := (Provider{}).Spec()
    if spec.Type == "" || len(spec.Fields) == 0 {
        t.Fatal("invalid spec")
    }
}
```

引擎集成：在 `engine_test.go` 的 fake catalog 中加入你的表，验证 `SELECT ... WHERE ... ORDER BY ...`。真实连接测试建议放到集成测试并用环境变量控制，避免 CI 依赖外部服务。

## 7. 内置连接器位置

| 位置 | 数据源 |
| --- | --- |
| `internal/sqlconn` | PostgreSQL、CockroachDB、Redshift、TimescaleDB、YugabyteDB、MySQL、MariaDB、SingleStore、SQL Server、SQLite、ClickHouse、Trino、Presto、Oracle |
| `internal/presets` | 37 个 SQL 兼容预设 + 32 个 REST/SaaS 模板（Aurora、Cloud SQL、Neon、TiDB、GitHub、Slack、Stripe 等） |
| `internal/cloudsql` | Snowflake、Databricks SQL |
| `internal/openapi` | OpenAPI 3 / Swagger 2 operations as tables |
| `internal/graphql` | GraphQL query/variables/columns/cursor pagination |
| `internal/bigquery` | Google BigQuery（REST） |
| `internal/cassandra` | Apache Cassandra / ScyllaDB（CQL） |
| `internal/mongodb` | MongoDB |
| `internal/influxdb` | InfluxDB |
| `internal/sls` | Alibaba Cloud SLS |
| `internal/tdengine` | TDengine |
| `internal/druid` | Apache Druid |
| `internal/excel` | Microsoft Excel |
| `internal/accessdb` | Microsoft Access (mdbtools) |
| `internal/redisdb` | Redis |
| `internal/elasticsearch` | Elasticsearch、OpenSearch |
| `internal/httpjson` | REST / JSON、分页、OAuth2、CSV URL、JSON Lines |
| `internal/httpauth` | 通用 OAuth2 client credentials / refresh token |
