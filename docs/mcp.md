# MCP + PAT scopes

AnyQuery exposes a **Streamable HTTP MCP server** at `/mcp`. MCP clients authenticate with a workspace **personal access token (PAT)** and only see/execute tools allowed by that token's scopes.

> Browser session cookies are intentionally **not** accepted by `/mcp`.

## 1. Create a PAT

Open **Workspace → Settings → API keys**, create a token and select scopes:

| Scope | Grants |
| --- | --- |
| `catalog:read` | List data sources, tables and columns; `get_catalog` MCP tool |
| `query:execute` | Execute federated SQL; `execute_sql` MCP tool |
| `query:read` | Read saved queries and query history; `list_saved_queries` MCP tool |

The raw token (`aq_…`) is shown once. Store it like a password. A token with no scopes is treated as a legacy full-access key; new tokens always have explicit scopes.

## 2. Endpoint

```
POST http://your-anyquery-host/mcp
Authorization: Bearer aq_...
Content-Type: application/json
```

Example client configuration (generic JSON):

```json
{
  "mcpServers": {
    "anyquery": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer aq_xxxxxxxxxxxxxxxxx"
      }
    }
  }
}
```

Claude Code:

```bash
claude mcp add --transport http anyquery http://localhost:8080/mcp \
  --header "Authorization: Bearer aq_xxxxxxxxxxxxxxxxx"
```

The server is stateless and returns JSON responses. `initialize`, `ping`, `tools/list` and `tools/call` are supported; `notifications/*` are accepted silently. An `Mcp-Session-Id` is returned on initialize for clients that require one, but it is not required on later requests.

## 3. Tools

### `get_catalog`

Scope: `catalog:read`

Returns data sources, tables and columns. Optional `source` filters to one data source.

```json
{"source": "mongo"}
```

### `execute_sql`

Scope: `query:execute`

Runs AnyQuery SQL, including cross-source joins. `limit` controls how many rows are returned to the model (default 200, max 1000); the execution row cap still applies.

```json
{
  "sql": "SELECT p.category, sum(o.total) AS revenue FROM mongo.orders o JOIN shop.products p ON o.product_id = p.id WHERE o.status = ? GROUP BY p.category",
  "params": ["paid"],
  "limit": 100
}
```

`params` is a positional JSON array for `?` or `$n` placeholders.

Every MCP execution is recorded in query history. Query timeouts and source row guards apply.

### `list_saved_queries`

Scope: `query:read`

Returns the saved queries visible to the token owner (team + own private queries), including SQL.

## 4. curl smoke test

```bash
KEY=aq_...

curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}'

curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute_sql","arguments":{"sql":"SELECT 1 AS ok"}}}'
```

## 5. Scope enforcement

- REST endpoints check scopes for PATs as well:
  - `catalog:read` → datasource/catalog endpoints
  - `query:execute` → `/queries/execute`
  - `query:read` → saved queries, runs/overview
- A missing scope returns HTTP `403 scope_required` for REST, or an MCP tool result with `isError: true`.
- `tools/list` only returns tools the token can call, so MCP clients never see unauthorized tools.
- Tokens are workspace-bound and cannot access another tenant.
- Revoke tokens from Settings at any time.

## 6. Limitations

- No OAuth-based MCP authorization yet; use PAT.
- Stateless Streamable HTTP only (no long-lived SSE session).
- MCP write/DDL tools are intentionally absent; the platform is read-only.
- Token rotation is manual: create a new token, update the client, revoke the old one.
