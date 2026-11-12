# OpenAPI / Swagger as a data source

The `openapi` connector reads an OpenAPI 3 or Swagger 2 document and exposes every
GET operation as a table.

## Configuration

| Field | Description |
| --- | --- |
| `spec` | Spec URL, or absolute path to a local JSON/YAML file |
| `spec_headers` | Optional JSON headers for a protected spec URL (falls back to `headers`) |
| `base_url` | Optional override for `servers[0].url` / Swagger `host` |
| `headers` | JSON object sent to API requests |
| `path_params` | JSON object used to substitute `{param}` path segments |
| `query_params` | JSON object added to every API request |
| `allow_methods` | Methods to expose, default `GET`; enable `GET,POST` for search APIs |
| `body_template` | JSON request body for POST/PUT/PATCH; `{name}` values come from path/query/header params |
| `include_tags` | Optional comma-separated operation tag allowlist |
| `exclude_tags` | Optional comma-separated operation tag denylist |
| `max_operations` | Maximum exposed operations (default 500) |
| `auto_paginate` | Follow page/offset/cursor pagination (default true) |
| `page_size` | Page-size value when a page-size query parameter is detected (default 100) |
| `max_pages` | Maximum pages fetched for one table scan (default 10) |

```json
{
  "spec": "https://api.example.com/openapi.yaml",
  "headers": {"Authorization": "Bearer ..."},
  "path_params": {"owner": "acme", "repo": "analytics"},
  "query_params": {"per_page": 100}
}
```

Operations with required path, query or header parameters are only exposed when
every required value is configured (`path_params`, `query_params`, `headers`).
This keeps the table list honest: a source does not advertise endpoints that
cannot be called. Pagination parameters are exempt because they are filled
automatically.

## Query

If the operation is `listPets`:

```sql
SELECT * FROM `pets_api`.`listpets` LIMIT 100;
```

Schema columns are inferred from `responses.200.content.*json.schema`
(or Swagger 2 `responses.200.schema`), including `$ref`, `allOf`, `oneOf`,
OpenAPI 3.1 nullable type arrays, nested wrappers and arrays. Common wrappers
such as `data`, `items`, `records`, `results`, `value` and `rows` are detected
automatically. If a response has no documented schema, columns are inferred from
the first fetched object.

Pagination is auto-detected from operation query parameters (`limit`,
`page_size`, `per_page`, `page`, `offset`, `skip`, cursor/token names) and from
common next-link/next-token response fields, plus HTTP `Link: <...>; rel="next"`
headers. `max_pages` guards runaway pagination; `truncated` is set when the page
guard is hit. Transient 429/502/503/504 responses are retried up to three times,
honoring `Retry-After`.

## Current limitations

- GET is the default. POST/PUT/PATCH/DELETE are opt-in via `allow_methods`;
  POST is useful for read-only search APIs, but you are responsible for the
  endpoint semantics.
- Pagination covers common limit/page/offset/cursor/Link patterns, but
  API-specific cursor formats may still need a custom connector.
- OAuth flows are not automated. Provide a bearer/API-key header, or refresh the
  header value when the token expires.
- Path, query and header parameters are connection-wide; create separate
  sources if you need multiple parameter sets.
- Security schemes and server variables beyond defaults are not automatically
  applied.
- Large specs can be narrowed with tag filters and `max_operations`.
