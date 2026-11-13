# SQL completeness audit

This page separates **the parser/AST** from **the execution engine** and records
what is actually supported, what is intentionally out of scope, and what still
needs work. The syntax reference is [sql.md](sql.md).

## Verdict

AnyQuery is a **read-only analytical SQL dialect**, not a drop-in replacement
for PostgreSQL/MySQL parsers.

| Scope | Rough completeness | Assessment |
| --- | --- | --- |
| AnyQuery analytical SELECT dialect | ~85% | Production-ready for the documented workload; remaining gaps are edge cases and advanced analytics |
| SQL-92 / SQL:2016 surface | ~35–40% | Core SELECT is strong; DML/DDL, full type system, JSON/array/interval and many dialect features are absent |
| DML / DDL / transactions | 0% by design | The engine and API are read-only |
| Parser as a reusable Go package | Good | Zero dependencies, typed errors, AST and formatter; exported AST is still experimental |

The percentages are approximate and relative to the target scope, not a claim of
standards certification.

## Support matrix

| Area | Status | Notes |
| --- | --- | --- |
| SELECT / DISTINCT / aliases / ordinals | ✅ | `DISTINCT ON` is explicitly rejected |
| FROM, comma joins, derived tables, inline VALUES | ✅ | Derived tables and VALUES tables require aliases; column alias lists on derived tables are not supported |
| INNER / LEFT / RIGHT / FULL / CROSS JOIN | ✅ | `ON`, `USING`, `NATURAL`; `NATURAL CROSS JOIN` is rejected |
| LATERAL / APPLY / UNNEST / table functions | ❌ | Not parsed |
| WHERE / HAVING | ✅ | Three-valued NULL logic is covered by engine tests |
| GROUP BY expressions, aliases, ordinals | ✅ | |
| ROLLUP / CUBE / GROUPING SETS | ✅ | Mixed base groups are normalized, e.g. `GROUP BY region, ROLLUP(status)` becomes `(region,status),(region)`; multiple grouping constructs are rejected |
| Window functions | 🟡 | Ranking/navigation/aggregates, `ROWS`/`RANGE`; no named windows, `WINDOW` clause, `GROUPS`, `EXCLUDE`, `WITHIN GROUP`, `IGNORE NULLS`, or interval RANGE bounds |
| Set operations | ✅ | `UNION`/`INTERSECT`/`EXCEPT`, `ALL`/`DISTINCT`, precedence, parentheses |
| CTEs / recursive CTEs | 🟡 | Multiple CTEs and `RECURSIVE`; one recursive UNION term, 100 iterations / 10,000 rows; no `MATERIALIZED`, `SEARCH` or `CYCLE` |
| Subqueries | ✅ | Scalar, `IN`, `EXISTS`, `ANY`/`SOME`/`ALL`; correlated subqueries execute per outer row |
| Basic expressions | ✅ | Arithmetic, concatenation, bitwise, comparison, `LIKE`/`ILIKE`/`REGEXP`, `IN`, `BETWEEN`, `IS`, `CASE`, `CAST`/`TRY_CAST`/chained `::`, typed date/time literals |
| JSON / arrays / maps / intervals | ❌ | No JSON operators, `ARRAY[...]`, subscripts, map access or `INTERVAL` values |
| `COLLATE`, `AT TIME ZONE`, `LIKE ... ESCAPE`, `SIMILAR TO` | ❌ | Not parsed |
| `DISTINCT ON`, `QUALIFY`, `TOP`, `FETCH ... WITH TIES` | ❌ | Not supported; unsupported forms must not silently change meaning |
| Bind parameters | 🟡 | `?` and `$n` work in expressions; `LIMIT`/`OFFSET`/frame bounds still require integer literals |
| SHOW / DESCRIBE / EXPLAIN | ✅ | Read-only metadata and plan statements |
| INSERT / UPDATE / DELETE / MERGE / DDL | ❌ | Rejected by design |
| Parser API | 🟡 | `Parse`, `Tokenize`, AST, formatter and typed `*sql.Error`; no source spans, multi-statement parse, dialect options or comment preservation |
| Formatter | 🟡 | Canonical output; qualified names/stars are quoted correctly, but some original spellings (e.g. ROLLUP vs expanded GROUPING SETS) are normalized |

## Reusable Go package

The lexer, parser, AST and formatter are a standalone Go module:

```text
github.com/anyquery/anyquery/sql
```

- No third-party dependencies.
- `backend` consumes it through a local `replace` directive.
- CI runs the module's vet/tests separately because it is a nested module.
- Import example:

```go
import anysql "github.com/anyquery/anyquery/sql"

stmt, err := anysql.Parse("SELECT region, count(*) FROM mongo.orders GROUP BY region")
if err != nil {
    return err
}
fmt.Println(anysql.FormatSelect(stmt.(*anysql.SelectStmt)))
```

Until a stable `sql/v1` tag exists, treat the exported AST as experimental:
new syntax may add node fields or node types. The parser entry point and
formatter are intended to stay stable.

## Audit fixes in this pass

- Extracted the parser into the reusable `github.com/anyquery/anyquery/sql` module.
- Added typed lexer/parser errors with line/column positions.
- Fixed formatter quoting for `source.table.*`, schema-qualified table names and
  identifiers that start with a digit.
- Added `TokenKind.String()` and bracket-identifier metadata.
- Added mixed base + ROLLUP/CUBE/GROUPING SETS normalization.
- Added `POSITION(substring, string)` convenience form and chained `::` casts.
- Rejected constructs that previously misparsed silently: `DISTINCT ON`,
  `ARRAY[...]`, numeric array subscripts, `NATURAL CROSS JOIN`, and multiple
  grouping constructs.
- Added parser fuzz coverage and CI fuzz smoke targets for lexer, parser and
  formatter round trips.

## Remaining work, in priority order

### P1 — user-visible query gaps

1. Parameterized `LIMIT`/`OFFSET` (requires widening the AST beyond `*int`).
2. Named windows, `WINDOW` clauses, `GROUPS`/`EXCLUDE`, `WITHIN GROUP`.
3. `LATERAL`, `UNNEST`, table functions, and derived-table column alias lists.
4. `DISTINCT ON`, `QUALIFY`, `TOP`, `FETCH ... WITH TIES`.
5. JSON/array/map/interval types and operators, `COLLATE`, `AT TIME ZONE`,
   `LIKE ... ESCAPE`, standard `TRIM(BOTH ... FROM ...)`.
6. Top-level `VALUES` / `TABLE name` statements.
7. PIVOT/UNPIVOT, semi/anti/ASOF joins, `MATCH_RECOGNIZE`.

### P2 — tooling and parser API

- Source spans on AST nodes for editors/linters.
- Multi-statement parsing and dialect/option configuration.
- Comment preservation in the formatter.
- More golden round-trip tests and a permanent fuzz corpus.
- Cost-based join ordering and streaming/spill execution (engine, not parser);
  see [sql.md](sql.md#execution-guards).

## Verification

```bash
cd sql && go vet ./... && go test ./...
cd backend && go test ./internal/engine ./internal/sqlconn ./internal/api
```

The parser tests cover core SELECT, joins, pipelines, modern SQL, grouping
sets, formatter round trips, public API behavior, typed errors and fuzzing.
Engine tests cover NULL semantics, joins, aggregation, windows, CTEs, set
operations and grouping sets.
