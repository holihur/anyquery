// Package sql implements AnyQuery's read-only analytical SQL dialect.
//
// It is a small, dependency-free lexer, recursive-descent/Pratt parser, AST
// and formatter. The dialect covers SELECT (joins, subqueries, CTEs, set
// operations, grouping sets, window functions), SHOW/DESCRIBE/EXPLAIN, and
// AnyQuery's FROM-first |> pipeline stages. It deliberately does not parse
// DML, DDL or transactions.
//
// Parsing one statement:
//
//	stmt, err := sql.Parse("SELECT region, count(*) FROM mongo.orders GROUP BY region")
//	if err != nil {
//		return err
//	}
//	sel := stmt.(*sql.SelectStmt)
//	fmt.Println(sql.FormatSelect(sel))
//
// Parse accepts one statement and an optional trailing semicolon. The AST is
// intentionally open for inspection and formatting, but the node interfaces
// use unexported marker methods: callers can type-switch over built-in nodes,
// not add new node types.
//
// The package is versioned independently as github.com/anyquery/anyquery/sql.
// Treat the exported AST as experimental until the module reaches a stable
// major version; parser additions may introduce new node fields.
package sql
