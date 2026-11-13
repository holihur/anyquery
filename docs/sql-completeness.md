# SQL completeness and conformance status

AnyQuery uses its own lexer/parser and an in-memory read-only relational engine.
The grammar is deliberately owned by the project so cross-source queries,
pipelines and source-specific syntax can evolve independently of
MySQL/Postgres drivers.

The frozen acceptance target is
[**SQL:2016 Read-Only Analytical Query Profile 1.0**](sql-2016-profile.md).
It is a project-defined, read-only query profile, not a claim of full SQL:2016
or SQL Server/T-SQL compatibility.

## Current verdict

| Scope | Status |
| --- | --- |
| AnyQuery SQL:2016 Read-Only Analytical Query Profile 1.0 | **100% of matrix rows supported** |
| `github.com/anyquery/anyquery/sql` statement coverage | **100.0%** |
| Fuzz smoke (lexer, parser, formatter round-trip) | **Green in CI** |
| Full SQL:2016 (DML/DDL, PSM, CLI, MED, XML, POLY, etc.) | **Out of scope** |
| SQL Server / T-SQL extensions (`TOP`, `PIVOT`, `QUALIFY`, ...) | **Out of scope** |

See the [profile matrix](sql-2016-profile.md#conformance-matrix) for the exact
row-by-row contract and test tags. A complete profile row requires parser,
engine, formatter and positive/boundary/error tests, not just syntax support.

## Supported query profile

- `SELECT`, aliases, expressions, derived tables, top-level `VALUES`/`TABLE`
- `JOIN` variants, `LATERAL`, `UNNEST`, `TABLESAMPLE`
- `WHERE`, `GROUP BY`, `HAVING`, `ROLLUP`/`CUBE`/`GROUPING SETS`
- `WITH`, recursive CTEs, set operations, subqueries
- window functions, named windows, `WINDOW`, `GROUPS`, `EXCLUDE`,
  `WITHIN GROUP`, ordered-set aggregates
- `LIMIT`/`OFFSET`/`FETCH`, parameterized row counts, `WITH TIES`
- `INTERVAL`, date/time/time-zone arithmetic, `AT TIME ZONE`
- SQL/JSON: `IS JSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_EXISTS`,
  `JSON_OBJECT`, `JSON_ARRAY`, `JSON_ARRAYAGG`, `JSON_OBJECTAGG`, `JSON_TABLE`
- read-only `FOR SYSTEM_TIME` temporal tables
- SQL:2016 `MATCH_RECOGNIZE`
- `SHOW`, `DESCRIBE`, `EXPLAIN` and AnyQuery `|>` pipelines

## Explicitly outside the profile

- `INSERT`, `UPDATE`, `DELETE`, `MERGE`
- DDL, transactions, savepoints and isolation levels
- SQL/PSM, SQL/CLI, SQL/MED, SQL/OLB, SQL/JRT, SQL/XML, SQL/POLY
- security, authorization, catalog evolution and stored routines
- user-defined scalar/table functions beyond the built-in registry
- vendor-only syntax such as `DISTINCT ON`, `QUALIFY`, `TOP`, `PIVOT`,
  `UNPIVOT`
- write-side temporal history maintenance and full array/map type systems

Unsupported syntax must produce an explicit parse/runtime error. It must not be
silently reinterpreted as a nearby supported form.

## Parser package quality gate

The parser is a standalone, dependency-free Go module:

```text
github.com/anyquery/anyquery/sql
```

The SQL module keeps **100.0% statement coverage**. Fuzz targets cover:

- `FuzzLexer`: token positions, EOF and malformed input
- `FuzzParse`: malformed input must never panic
- `FuzzFormatRoundTrip`: parse → format → parse must preserve valid syntax

CI runs a short smoke for every target and fails on a discovered reproducer.

## Known engine limitations

These do not reduce the Profile 1.0 row status, but matter for large or
non-standard workloads:

- execution is materialized and in-memory; no streaming/hash-join disk spill
- no cost-based join reordering; joins follow written order and choose the
  smaller inner-join build side
- window frame offsets are integer literals, not bind parameters
- UNNEST expands JSON/array values, not arbitrary user table functions
- temporal reads require a connector to advertise `Capabilities.SystemTime`
- interval and date/time values are normalized strings, not a full SQL type
  system

## Verification

```bash
cd sql
go vet ./...
go test ./... -count=1 -covermode=set -coverprofile=coverage.out
go tool cover -func=coverage.out | tail -1

go test -run=^$ -fuzz=FuzzLexer -fuzztime=5s
go test -run=^$ -fuzz=FuzzParse -fuzztime=5s
go test -run=^$ -fuzz=FuzzFormatRoundTrip -fuzztime=5s

cd ../backend
go test ./... -count=1
```

CI also runs backend build/tests, E2E MongoDB + SQLite federation, and the
production Docker image build.
