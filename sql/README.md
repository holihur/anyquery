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

- `SELECT` with joins, subqueries, CTEs (including `WITH RECURSIVE`), set
  operations, grouping sets and window functions
- `SHOW`, `DESCRIBE`, `EXPLAIN`
- AnyQuery `FROM ... |> WHERE ... |> AGGREGATE ...` pipelines
- `?` / `$n` bind parameters in expressions
- AST inspection, `FormatSelect` / `FormatTableRef` / `FormatPipe`
- Typed `*sql.Error` with line/column positions

Not supported by design: DML, DDL, transactions, PIVOT, LATERAL, JSON/array
operators and the full SQL type system. See
[docs/sql-completeness.md](../docs/sql-completeness.md) and
[docs/sql.md](../docs/sql.md).

## Development

This is a nested Go module:

```bash
cd sql
go test ./...
go vet ./...
go test -run '^$' -fuzz FuzzParse -fuzztime=10s
```

The backend uses a local `replace` directive, so changes here are picked up
immediately by `cd ../backend && go test ./...`.

## Stability

`Parse` and the formatter are the stable entry points. The exported AST is
currently experimental; new syntax may add fields or node types. A stable
`sql/v1` tag will be published after the AST surface settles.
