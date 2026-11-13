# SQL:2016 read-only analytical query profile

This document freezes **AnyQuery SQL:2016 Read-Only Analytical Query Profile
1.0**. It is the acceptance target for the 100% SQL analysis work. It is not a
claim that AnyQuery implements every part of SQL:2016.

## Definition of done

A matrix row is **supported** only when all of the following exist:

1. Parser/AST support in `github.com/anyquery/anyquery/sql`.
2. Engine execution semantics in `backend/internal/engine`.
3. Canonical formatter support that can be parsed again.
4. Positive, boundary and error tests.
5. The row's test tag is present and green in CI.

The profile is **100% complete** when every row in the matrix is `supported`,
the `sql` module has 100% statement coverage, and all fuzz targets have green
CI smoke runs. A row marked `partial` or `planned` is not part of the completed
claim.

## In scope

Read-only SQL:2016 query language and the analytical features needed to execute
queries against AnyQuery sources:

- query expressions, table expressions, derived tables and `VALUES`
- joins, `LATERAL`, `UNNEST`, table functions and `TABLESAMPLE`
- `WITH`, recursive CTEs, set operations
- grouping, grouping sets and ordered-set aggregates
- window functions, named windows, `WINDOW`, `GROUPS`, `EXCLUDE`, `WITHIN GROUP`
- `FETCH FIRST ... WITH TIES`, `OFFSET`, parameterized row counts
- SQL/JSON query and constructor functions, including `JSON_TABLE`
- `INTERVAL`, date/time/time-zone semantics and standard casts
- SQL:2016 temporal table queries (`FOR SYSTEM_TIME` forms defined below)
- SQL:2016 row pattern recognition (`MATCH_RECOGNIZE`)
- standard string, numeric, conditional and aggregate functions used by the
  above

## Out of scope

- `INSERT`, `UPDATE`, `DELETE`, `MERGE`
- DDL, transactions, savepoints and isolation levels
- SQL/PSM, SQL/CLI, SQL/MED, SQL/OLB, SQL/JRT
- SQL/XML and SQL/POLY
- security, authorization, catalogs and schema evolution
- user-defined types, user-defined functions and stored routines
- vendor extensions such as `DISTINCT ON`, `QUALIFY`, `TOP`, `PIVOT` and
  `UNPIVOT` unless they are added by a separate profile
- write-side temporal history maintenance; temporal reads are in scope, writes
  are not

## Conformance matrix

Status values:

- `supported`: parser, engine, formatter and tests exist.
- `partial`: some layers exist, but the row does not meet the definition above.
- `planned`: not implemented.
- `excluded`: explicitly outside this profile.

The test tag is the Go subtest under
`backend/internal/engine/TestSQL2016ReadOnlyProfileMatrix`. Pending rows use
`pending` until the implementation lands.

| ID | Feature | Status | Test tag |
| --- | --- | --- | --- |
| CORE-001 | SELECT list, aliases, expressions | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-001` |
| CORE-002 | FROM, table refs, comma joins | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-002` |
| CORE-003 | INNER/LEFT/RIGHT/FULL/CROSS JOIN with ON/USING/NATURAL | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-003` |
| CORE-004 | Derived tables and inline VALUES | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-004` |
| CORE-005 | Derived table column alias lists | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-005` |
| CORE-006 | Top-level VALUES / TABLE statements | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-006` |
| CORE-007 | LATERAL derived tables | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-007` |
| CORE-008 | UNNEST and table functions | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-008` (UNNEST; arbitrary user table functions are out of profile) |
| CORE-009 | TABLESAMPLE | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-009` |
| CORE-010 | WHERE / HAVING | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-010` |
| CORE-011 | GROUP BY expressions, aliases and ordinals | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-011` |
| CORE-012 | ROLLUP / CUBE / GROUPING SETS, including mixed base groups | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-012` |
| CORE-013 | Set operations UNION/INTERSECT/EXCEPT with ALL/DISTINCT | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-013` |
| CORE-014 | WITH and non-recursive CTEs | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-014` |
| CORE-015 | WITH RECURSIVE | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-015` |
| CORE-016 | Subqueries: scalar, IN, EXISTS, ANY/SOME/ALL | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-016` |
| CORE-017 | LIMIT/OFFSET and FETCH FIRST | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-017` |
| CORE-018 | Parameterized LIMIT/OFFSET/fetch counts | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-018` |
| CORE-019 | FETCH FIRST ... WITH TIES | supported | `TestSQL2016ReadOnlyProfileMatrix/CORE-019` |
| WIN-001 | Ranking/navigation/aggregate window functions | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-001` |
| WIN-002 | ROWS/RANGE frames | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-002` |
| WIN-003 | Named windows and WINDOW clause | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-003` |
| WIN-004 | GROUPS frames and EXCLUDE | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-004` |
| WIN-005 | WITHIN GROUP and ordered-set aggregates | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-005` |
| WIN-006 | FILTER and DISTINCT in windows | supported | `TestSQL2016ReadOnlyProfileMatrix/WIN-006` |
| EXPR-001 | Arithmetic, comparison, boolean, NULL and IS predicates | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-001` |
| EXPR-002 | CASE, CAST, TRY_CAST and chained `::` | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-002` |
| EXPR-003 | LIKE/ILIKE/REGEXP, BETWEEN, IN and quantified comparisons | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-003` |
| EXPR-004 | Standard string functions, including TRIM/OVERLAY | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-004` |
| EXPR-005 | INTERVAL values and arithmetic | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-005` |
| EXPR-006 | Date/time/time-zone semantics | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-006` |
| EXPR-007 | Bound parameters in expressions | supported | `TestSQL2016ReadOnlyProfileMatrix/EXPR-007` |
| JSON-001 | IS JSON | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-001` |
| JSON-002 | JSON_VALUE and JSON_QUERY | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-002` |
| JSON-003 | JSON_EXISTS | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-003` |
| JSON-004 | JSON_OBJECT / JSON_ARRAY constructors | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-004` |
| JSON-005 | JSON_ARRAYAGG / JSON_OBJECTAGG | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-005` |
| JSON-006 | JSON_TABLE | supported | `TestSQL2016ReadOnlyProfileMatrix/JSON-006` |
| TEMP-001 | Temporal table reads with FOR SYSTEM_TIME | supported | `TestSQL2016ReadOnlyProfileMatrix/TEMP-001` |
| RPR-001 | MATCH_RECOGNIZE | supported | `TestSQL2016ReadOnlyProfileMatrix/RPR-001` |
| META-001 | SHOW / DESCRIBE / EXPLAIN | supported | `TestSQL2016ReadOnlyProfileMatrix/META-001` |
| AQ-001 | FROM-first `|>` analytical pipelines | supported | `TestSQL2016ReadOnlyProfileMatrix/AQ-001` |
| OUT-001 | DML, DDL and transactions | excluded | `n/a` |

## Temporal forms

The profile freezes these read-only temporal forms once the connector contract
supports them:

```sql
SELECT ... FROM table FOR SYSTEM_TIME AS OF TIMESTAMP '...'
SELECT ... FROM table FOR SYSTEM_TIME FROM TIMESTAMP '...' TO TIMESTAMP '...'
SELECT ... FROM table FOR SYSTEM_TIME BETWEEN TIMESTAMP '...' AND TIMESTAMP '...'
SELECT ... FROM table FOR SYSTEM_TIME ALL
```

A connector that cannot provide the requested history must fail with an
explicit capability error. It must not silently return current rows.

## Row pattern recognition

The `MATCH_RECOGNIZE` row covers the SQL:2016 query feature with at least:

```sql
MATCH_RECOGNIZE (
  PARTITION BY ...
  ORDER BY ...
  MEASURES ...
  ONE ROW PER MATCH | ALL ROWS PER MATCH
  PATTERN (...)
  DEFINE ...
)
```

Unsupported pattern operators or clauses must produce an explicit error.

## Executable matrix

`backend/internal/engine/sql2016_profile_test.go` owns the current case list.
Each supported row runs the full parser → engine → formatter → reparse path.
Planned rows are skipped with a reason until their implementation changes the
status to `supported`.

Run it with:

```bash
cd backend
go test ./internal/engine -run TestSQL2016ReadOnlyProfileMatrix -v
```

## Maintenance rules

- Do not mark a row `supported` based only on a parser test.
- Do not add a new row without a test tag or `pending`.
- Do not silently reinterpret an unsupported SQL construct as a nearby feature.
- Keep this matrix and the implementation tests in the same change.
- The profile boundary is frozen; changing it requires a user-approved goal
  tweak, not an implementation shortcut.
