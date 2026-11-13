# GraphQL 数据源

`graphql` 连接器把 GraphQL endpoint 变成一张或多张 SQL 表。GraphQL 没有统一
的“表”概念，因此每张表对应一条已配置的 GraphQL **query**；连接器负责发送请求、
展开 `data`、分页和列类型。

## 最小示例

```json
{
  "endpoint": "https://api.example.com/graphql",
  "headers": {"Authorization": "Bearer ..."},
  "query": "query($limit: Int) { users(limit: $limit) { id name email } }",
  "table_name": "users",
  "data_path": "users",
  "columns": {
    "id": "integer",
    "name": "string",
    "email": "string"
  }
}
```

```sql
SELECT id, name FROM graphql.users WHERE name LIKE 'A%';
```

`data_path` 是 GraphQL 响应 `data` 对象内部的点路径。留空时会自动识别：

- 根数组；
- 单根字段，例如 `{"data":{"users":[...]}}`；
- `nodes` / `items` / `records` / `results` / `rows` / `edges` 等常见包装；
- 唯一数组字段（即使字段名不在上述列表中）。

## 多条 GraphQL 查询

`queries` 是一个 JSON 对象，key 是 SQL 表名：

```json
{
  "endpoint": "https://api.example.com/graphql",
  "headers": {"Authorization": "Bearer ..."},
  "queries": {
    "users": {
      "query": "query($limit: Int) { users(limit: $limit) { id name } }",
      "data_path": "users",
      "columns": {"id": "integer", "name": "string"}
    },
    "orders": {
      "query": "query($limit: Int, $after: String) { orders(first: $limit, after: $after) { nodes { id total } pageInfo { hasNextPage endCursor } } }",
      "data_path": "orders.nodes",
      "columns": {"id": "integer", "total": "number"},
      "limit_variable": "limit",
      "cursor_variable": "after",
      "cursor_path": "orders.pageInfo.endCursor",
      "has_next_path": "orders.pageInfo.hasNextPage"
    }
  }
}
```

单条查询的字段（`variables`、`data_path`、`columns`、分页字段等）可以作为顶层
默认值，供所有命名查询继承，也可以在每条查询里覆盖。

## 变量与行数限制

- `variables` 是 GraphQL variables 的 JSON 对象。
- 如果 query 中引用了 `$limit`，且 variables 没有显式给 `limit`，连接器会把 SQL
  扫描请求的行数上限写入该变量。变量名可用 `limit_variable` 修改。
- `max_pages` 默认 10，最大 100；达到上限时会标记 `truncated`。

## 分页

支持 cursor 分页：

| 字段 | 作用 |
| --- | --- |
| `cursor_variable` | 每页请求前写入下一页 cursor 的 GraphQL 变量名 |
| `cursor_path` | `data` 内的 cursor 点路径，或最后一行的字段路径 |
| `has_next_path` | 可选的布尔路径，为 `false` 时停止 |

如果 cursor 为空或与上一页相同，连接器会停止，避免无限循环。

## 列

`columns` 可以是：

```json
{"id": "integer", "name": "string"}
```

也可以是列名数组：

```json
["id", "name"]
```

支持的逻辑类型：`string`、`integer`、`number`、`boolean`、`timestamp`、
`object`、`array`。未声明列时，连接器会从 GraphQL query 的 selection set
自动提取字段（支持 alias、`nodes`/`edges`/`node` 包装），首次打开 Schema
即可看到列；实际取数后仍会根据返回行补充缺失字段。复杂 fragment 无法静态
解析时，建议显式声明 `columns`。

## Linear preset

内置 `linear` 类型使用 GraphQL connector，预置了 Linear Issues 查询、cursor 分页和列声明：

- Endpoint: `https://api.linear.app/graphql`
- Header: `{"Authorization":"Bearer <LINEAR_API_KEY>"}`
- 默认表：`issues`
- 分页：`after` + `pageInfo`

它仍标记为 `template`，因为尚未用真实 Linear workspace 做 CI 验证；可以直接修改 query 适配自己的团队字段。

## HTTP 与认证

- 默认使用 `POST`；设置 `"http_method": "GET"` 可改用 GraphQL over HTTP GET。
- `headers` 是 JSON 对象，适合 `Authorization: Bearer ...`、API key 等。
- 支持 OAuth2 `client_credentials` / `refresh_token`：配置 `auth_type`、`token_url`、`client_id`、`client_secret`、`refresh_token`、`scope`。
- Header 和 OAuth secret 加密保存，API 返回时脱敏。
- 429/502/503/504 会重试最多 3 次，并遵守 `Retry-After`。
- 响应上限 64MB；请求超时 60 秒。

## 只读约束

连接器只接受 `query` 操作或匿名 `{ ... }` 查询，拒绝 `mutation` 和
`subscription`。GraphQL resolver 自身仍可能产生副作用，请使用只读 token 和
只读 endpoint。

## 当前限制

- 不做 GraphQL schema 自动发现；需要显式配置 query 和列。
- 复杂 required arguments、嵌套对象展开、Relay connection 的自动分页尚未内置；
  可用 `data_path`、`cursor_*` 和显式 variables 适配。
- 一次扫描对应一条 query；跨表 JOIN 由 AnyQuery 引擎完成。
