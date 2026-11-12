# OpenAPI / Swagger as a data source

The `openapi` connector reads an OpenAPI 3 or Swagger 2 document and exposes every
GET operation as a table.

## Configuration

| Field | Description |
| --- | --- |
| `spec` | Spec URL, or absolute path to a local JSON/YAML file |
| `base_url` | Optional override for `servers[0].url` / Swagger `host` |
| `headers` | JSON object sent to both the spec request and API requests |
| `path_params` | JSON object used to substitute `{param}` path segments |
| `query_params` | JSON object added to every API request |

```json
{
  "spec": "https://api.example.com/openapi.yaml",
  "headers": {"Authorization": "Bearer ..."},
  "path_params": {"owner": "acme", "repo": "analytics"},
  "query_params": {"per_page": 100}
}
```

Operations with required path parameters are only exposed when every required
parameter is present in `path_params`. This keeps the table list honest: a
source does not advertise endpoints that cannot be called.

## Query

If the operation is `listPets`:

```sql
SELECT * FROM `pets_api`.`listpets` LIMIT 100;
```

Schema columns are inferred from `responses.200.content.application/json.schema`
(or Swagger 2 `responses.200.schema`). Array responses and common wrappers such
as `data`, `items`, `records`, `results`, `value` and `rows` are detected
automatically. If a response has no documented schema, columns are inferred from
the first fetched object.

## Current limitations

- GET operations only; the platform is read-only.
- One page per request; server-side pagination is not implemented yet.
- OAuth flows are not automated. Provide a bearer/API-key header, or refresh the
  header value when the token expires.
- Path parameters are connection-wide; create separate sources if you need
  multiple parameter sets.
- Security schemes and server variables beyond defaults are not automatically
  applied.
