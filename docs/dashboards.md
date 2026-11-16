# Dashboards and visualizations

Dashboards turn saved analyses into a shared, live view. Each **panel** owns a
read-only SQL query and a visualization configuration; the panel executes via
the same federated engine and tenant catalog as Query Studio.

## Permissions

| Role | View dashboards | Create / edit / delete |
| --- | --- | --- |
| owner / admin / member | ✅ | ✅ |
| viewer | ✅ | — |

Dashboard writes are session-only, like saved-query writes: workspace API keys
can read dashboards with `query:read` but cannot mutate them. Changes are
recorded in the audit log as `dashboard.create`, `dashboard.update` and
`dashboard.delete`.

## Limits

- `MaxDashboardsPerWorkspace`: 100
- `MaxPanelsPerDashboard`: 24
- Panel SQL: 200,000 bytes
- Dashboard name: 120 characters; description: 1,000 characters
- Layout is a 12-column grid; width is clamped to 1–12 and height to 1–12.

## Panel model

```json
{
  "id": "p_...",
  "title": "Revenue by region",
  "sql": "SELECT region, sum(total) AS revenue FROM mongo.orders GROUP BY region",
  "visualization": {
    "type": "bar",
    "x": "region",
    "y": ["revenue"]
  },
  "layout": { "x": 0, "y": 0, "w": 6, "h": 4 }
}
```

Supported visualization types:

| Type | Configuration |
| --- | --- |
| `table` | No axis configuration; compact result table |
| `bar`, `line`, `area` | `x` category/time column + one or more numeric `y` columns |
| `pie` | `x` category + first `y` column; values are aggregated by category |
| `scatter` | Numeric `x` + first numeric `y` |
| `stat` | First `y` value rendered as a KPI |

Charts are rendered as SVG in the browser; no query result is sent to a chart
SaaS. Auto-refresh re-runs every panel at the selected interval, so use it with
bounded queries (`LIMIT`, filters or rollups).

## API

```
GET    /api/v1/tenants/{tenant}/dashboards
POST   /api/v1/tenants/{tenant}/dashboards
GET    /api/v1/tenants/{tenant}/dashboards/{id}
POST   /api/v1/tenants/{tenant}/dashboards/{id}/run
PATCH  /api/v1/tenants/{tenant}/dashboards/{id}
DELETE /api/v1/tenants/{tenant}/dashboards/{id}
```

The PATCH endpoint replaces the dashboard metadata and the complete panel
array. The UI persists panel add/edit/delete/reorder changes immediately.

`POST /dashboards/{id}/run` executes every panel in one request with a
six-panel server-side concurrency limit. It returns one result/error per panel,
uses the tenant query cache and singleflight, and never writes dashboard
refreshes to query history. The UI uses this endpoint for the grid and manual
refresh.
