# Loading performance

## Frontend

- **Route-level code splitting** via `React.lazy`/`Suspense`: login and app shell load first; query studio, admin pages and settings load on demand.
- **Heavy editor deferred**: CodeMirror and the SQL formatter are only loaded when the query studio route is opened. `sql-formatter` is dynamically imported when **Format** is clicked.
- **Locale-aware Intl** uses the selected locale without shipping extra date libraries.
- **Fonts** use `font-display: swap`; browsers fetch only the unicode subsets they need.

Current production bundle shape (after the split):

| Asset | Raw | Gzip |
| --- | ---: | ---: |
| Main app | ~327 KB | ~102 KB |
| Query studio chunk | ~478 KB | ~154 KB |
| Admin page chunks | 7–20 KB each | 2–7 KB each |
| CSS | ~85 KB | ~17 KB |

The query studio chunk is not part of the login/overview critical path.

## Backend

- Static assets are served with **gzip** when the client supports it (JavaScript, CSS, HTML, JSON, SVG; images/fonts are skipped).
- Vite hashed files under `/assets/` are served with:

  ```http
  Cache-Control: public, max-age=31536000, immutable
  ```

- `index.html` and SPA fallbacks use `Cache-Control: no-cache` so a new deployment is picked up immediately.
- API JSON responses are also compressed when `Accept-Encoding: gzip` is present.

A reverse proxy/CDN can keep the same headers; the immutable asset URLs make repeat visits nearly free.

## Measuring

```bash
cd frontend && pnpm build
cd ../backend && go build -o /tmp/anyquery ./cmd/server
curl -sI -H 'Accept-Encoding: gzip' http://localhost:8080/assets/<hashed>.js
curl -s -H 'Accept-Encoding: gzip' -o /dev/null -w '%{size_download}\n' \
  http://localhost:8080/assets/<hashed>.js
```

Use Lighthouse/Chrome DevTools with cache disabled to check the first load, then repeat with cache enabled to verify immutable caching.

## Query result cache

Successful query results are cached in process for `QUERY_CACHE_TTL_SECONDS`
(default 15 seconds). The cache is tenant-scoped and keyed by the exact SQL text
plus bind parameters. Identical concurrent queries are coalesced
(singleflight), so a dashboard with duplicate panels only opens one source
query on a cache miss.

- `QUERY_CACHE_TTL_SECONDS=0` disables the cache and coalescing.
- `QUERY_CACHE_MAX_ENTRIES` bounds the LRU (default 200).
- `QUERY_CACHE_MAX_MB` bounds the approximate retained row size (default 64). A
  single oversized result is not cached.
- Creating, editing, deleting, refreshing or re-authorizing a data source
  invalidates that workspace's cache immediately.
- Errors are not cached. Query results are immutable snapshots; cache hits are
  recorded on query runs as `cacheHit: true`.
- Tenant source-catalog snapshots are held for 5 seconds while caching is
  enabled, avoiding a MongoDB read per dashboard panel. Data-source mutations
  invalidate the snapshot immediately.
- Dashboard panels run through one batch endpoint with bounded server-side
  concurrency (6), instead of one HTTP request per panel.
- Dashboard refreshes use the batch endpoint, which does not write query-run
  documents. The UI falls back to per-panel execution against older backends.
- Results are not invalidated by changes made directly in the source database;
  tune the TTL for freshness. Non-deterministic expressions such as
  `CURRENT_TIMESTAMP` share the cache window.
