# AnyQuery SQL parser

A dependency-free, read-only analytical SQL lexer, parser, AST and formatter
used by [AnyQuery](https://github.com/holihur/anyquery).

```go
import anysql "github.com/anyquery/anyquery/sql"

stmt, err := anysql.Parse(`
  SELECT region, count(*) AS orders
  FROM mongo.orders
  WHERE status = ?
  GROUP BY region
  ORDER BY orders DESC
  LIMIT 10
`)
if err != nil {
    return err
}
fmt.Println(anysql.FormatSelect(stmt.(*anysql.SelectStmt)))
```

## Scope

Supported:

- `SELECT` with joins, `LATERAL`, `UNNEST`, `TABLESAMPLE`, subqueries, CTEs,
  set operations, grouping sets and named/window functions
- SQL/JSON: `IS JSON`, `JSON_VALUE/QUERY/EXISTS`, constructors, aggregates and
  `JSON_TABLE`
- `INTERVAL`, date/time/time-zone arithmetic and `AT TIME ZONE`
- read-only `FOR SYSTEM_TIME` temporal tables and `MATCH_RECOGNIZE`
- `SHOW`, `DESCRIBE`, `EXPLAIN`
- AnyQuery `FROM ... |> WHERE ... |> AGGREGATE ...` pipelines
- `?` / `$n` bind parameters in expressions
- AST inspection, formatters and typed `*sql.Error` with line/column positions

Not supported by design: DML, DDL, transactions, `PIVOT`, vendor-only
`DISTINCT ON`/`QUALIFY`/`TOP`, arbitrary user-defined table functions and the
full SQL type system. See [docs/sql-completeness.md](../docs/sql-completeness.md)
and [docs/sql.md](../docs/sql.md).

## Development

This is a nested Go module:

```bash
cd sql
go test ./...
go vet ./...
go test -run '^$' -fuzz FuzzLexer -fuzztime=10s
go test -run '^$' -fuzz FuzzParse -fuzztime=10s
go test -run '^$' -fuzz FuzzFormatRoundTrip -fuzztime=10s
```

Fuzz targets cover the lexer, parser no-panic behavior, and formatter
parse→format→parse round trips. CI runs a short smoke for each target.

The backend uses a local `replace` directive, so changes here are picked up
immediately by `cd ../backend && go test ./...`.

## Stability

`Parse` and the formatter are the stable entry points. The exported AST is
currently experimental; new syntax may add fields or node types. A stable
`sql/v1` tag will be published after the AST surface settles.
