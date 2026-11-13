# SQL support

AnyQuery uses its own lexer/parser and an in-memory relational engine. The grammar is deliberately owned by the project so cross-source queries, pipelines and source-specific syntax can evolve independently of MySQL/Postgres drivers.

> The lexer/parser/AST/formatter is also a standalone, dependency-free Go module: `github.com/anyquery/anyquery/sql`. See [sql-completeness.md](sql-completeness.md) for the feature matrix, audit findings and remaining gaps.

## Statements

```sql
SELECT ... [UNION ...] [ORDER BY ...] [LIMIT ...]
WITH name AS (SELECT ...) SELECT ...
SHOW TABLES [FROM source]
SHOW COLUMNS FROM source.table
SHOW SOURCES
DESCRIBE source.table
EXPLAIN SELECT ...
```

AnyQuery is read-only: `INSERT`, `UPDATE`, `DELETE`, DDL and transactions are not accepted.

## SELECT

- `SELECT [ALL|DISTINCT]`, arbitrary expressions, aliases (`AS` optional)
- `FROM` tables, comma-separated cross joins, derived tables, CTEs
- `WHERE`, `GROUP BY`, `HAVING`, `ORDER BY`, `LIMIT/OFFSET`, `FETCH FIRST n ROWS ONLY`, `LIMIT ALL`
- `ORDER BY` supports aliases, select-list ordinals (`ORDER BY 1`) and `NULLS FIRST|LAST` (default: nulls last)
- `GROUP BY` supports aliases and select-list ordinals
- `INNER / LEFT / RIGHT / FULL / CROSS JOIN`, `ON` / `USING`, `NATURAL [LEFT|RIGHT|FULL] JOIN`
- `USING` / `NATURAL` common columns are coalesced in `SELECT *`; qualified references such as `right_alias.id` remain available

Table names use `` `source`.`table` ``. A table name may contain a schema: `` `pg`.`public.users` ``. CTE names are referenced without a source.

### Set operations

```sql
SELECT region FROM mongo.orders
UNION
SELECT region FROM mongo.customers
ORDER BY region;

SELECT region FROM orders INTERSECT SELECT region FROM customers;
SELECT region FROM orders EXCEPT SELECT region FROM customers;
```

- `UNION`, `INTERSECT`, `EXCEPT`; `ALL` and `DISTINCT` variants
- `INTERSECT` has higher precedence than `UNION`/`EXCEPT`
- parentheses can group set terms
- output columns come from the left term; both sides must have the same column count

### Grouping sets

```sql
SELECT region, category, sum(total) AS revenue
FROM mongo.orders
GROUP BY ROLLUP(region, category);

SELECT region, count(*) AS n, grouping(region) AS g
FROM mongo.orders
GROUP BY GROUPING SETS ((region), ());
```

`ROLLUP`, `CUBE` and explicit `GROUPING SETS` are supported, along with `GROUPING()` / `GROUPING_ID()`. Omitted grouping columns are returned as `NULL`. `CUBE` accepts at most 8 expressions.

### Recursive CTEs

```sql
WITH RECURSIVE nums(n) AS (
  SELECT 1
  UNION ALL
  SELECT n + 1 FROM nums WHERE n < 5
)
SELECT n FROM nums ORDER BY n;
```

Recursive CTEs support `UNION` (global de-duplication) and `UNION ALL`. The recursive term sees only the previous iteration's rows. Guards: 100 iterations and 10,000 accumulated rows.

### Subqueries

```sql
-- scalar subquery
SELECT name,
       (SELECT count(*) FROM mongo.orders o WHERE o.customer_id = c.id) AS orders
FROM mongo.customers c;

-- IN / NOT IN
SELECT name FROM mongo.customers
WHERE id IN (SELECT customer_id FROM mongo.orders WHERE total > 100);

-- EXISTS / NOT EXISTS
SELECT name FROM mongo.customers c
WHERE EXISTS (SELECT 1 FROM mongo.orders o WHERE o.customer_id = c.id);

-- derived table
SELECT region, sum(n) AS n
FROM (SELECT region, count(*) AS n FROM mongo.orders GROUP BY region) t
GROUP BY region;

-- inline VALUES table
SELECT id, name FROM (VALUES (1, 'a'), (2, 'b')) AS t(id, name);
```

Subqueries can be correlated with the outer query. Correlated subqueries are executed per outer row, so keep their filters selective.

### Bind parameters

Positional parameters use `?` or `$n`:

```sql
SELECT * FROM mongo.orders
WHERE status = ? AND total > $2;
```

API request:

```json
{
  "sql": "SELECT * FROM mongo.orders WHERE status = ? AND total > $2",
  "params": ["paid", 100]
}
```

The Query Studio has a **Params** dialog that accepts a JSON array. MCP `execute_sql` accepts the same `params` array. Parameters are evaluated by the engine, not interpolated into source SQL.

### Quantified comparisons

```sql
SELECT * FROM mongo.orders
WHERE total > ANY (SELECT total FROM mongo.orders WHERE region = 'EU');

SELECT * FROM mongo.customers
WHERE id <> ALL (SELECT customer_id FROM mongo.orders);
```

`ANY`/`SOME` and `ALL` work with subqueries and parenthesized value lists, with SQL three-valued NULL semantics.

### Window functions

```sql
SELECT region, total,
       row_number() OVER (PARTITION BY region ORDER BY total DESC) AS rn,
       sum(total) OVER (PARTITION BY region ORDER BY total) AS running,
       lag(total) OVER (PARTITION BY region ORDER BY total) AS previous
FROM mongo.orders;
```

Supported:

- ranking: `row_number`, `rank`, `dense_rank`, `percent_rank`, `cume_dist`, `ntile`
- navigation: `lag`, `lead`, `first_value`, `last_value`, `nth_value`
- aggregates: `count`, `sum`, `avg`, `min`, `max`, `stddev*`, `variance*`, `group_concat`, `string_agg`, `array_agg`, `bool_and`, `bool_or`
- `PARTITION BY`, `ORDER BY`, and `ROWS`/`RANGE` frames

`RANGE` frames use peer groups for `UNBOUNDED`/`CURRENT ROW` bounds and value offsets for a single numeric `ORDER BY` key. Window functions can be combined with `GROUP BY`; aggregate projections are evaluated first and windows run over the grouped rows. `HAVING` is applied before window evaluation.

### Aggregate FILTER

```sql
SELECT region,
       count(*) AS all_orders,
       count(*) FILTER (WHERE status = 'paid') AS paid_orders,
       sum(total) FILTER (WHERE status = 'paid') AS paid_revenue
FROM mongo.orders
GROUP BY region;
```

`FILTER` works for aggregate and window aggregate functions.

## Expressions

- arithmetic: `+ - * / %`, `||` (concatenation), `& | ~`
- comparisons: `= != <> < <= > >=`, `LIKE`, `ILIKE`, `REGEXP`
- `IN (...)`, `ANY`/`SOME`/`ALL`, `BETWEEN`, `IS [NOT] NULL/TRUE/FALSE`, `IS [NOT] DISTINCT FROM`
- `CASE`, `CAST`, `TRY_CAST`, `::`, `EXISTS`, scalar subqueries
- typed literals: `DATE '2024-01-01'`, `TIMESTAMP '2024-01-01 12:00:00'`, `TIMESTAMPTZ '...'`
- bind parameters: `?`, `$1`
- `CURRENT_DATE`, `CURRENT_TIMESTAMP`, `NOW()`

## Functions

| Category | Functions |
| --- | --- |
| Strings | `lower`, `upper`, `length`, `trim`, `ltrim`, `rtrim`, `substring`/`substr`, `concat`, `concat_ws`, `replace`, `split_part`, `left`, `right`, `reverse`, `repeat`, `position`/`strpos`/`instr`, `starts_with`, `ends_with`, `contains`, `initcap`, `lpad`, `rpad`, `regexp_replace`, `regexp_extract` |
| Nulls / conditionals | `coalesce`, `ifnull`, `nvl`, `nullif`, `if`/`iff` |
| Math | `abs`, `ceil`/`ceiling`, `floor`, `round`, `sqrt`, `power`/`pow`, `mod`, `greatest`, `least` |
| Date / time | `now`, `current_date`, `current_timestamp`, `date_trunc`, `extract`, `date_part`, `date_add`/`dateadd`, `date_sub`/`datesub`, `date_diff`/`datediff`, `to_timestamp`, `timezone` |
| JSON / hashing | `json_extract`, `json_extract_scalar`, `md5`, `sha256` |
| Random | `random`/`rand` |

### SQL-standard shorthand

```sql
SELECT EXTRACT(year FROM created_at) AS y,
       SUBSTRING(name FROM 2 FOR 3) AS initials,
       POSITION('ac' IN name) AS pos
FROM mongo.customers;
```

## Pipelines

AnyQuery's `|>` stages remain available alongside standard SQL:

```sql
FROM mongo.orders
|> WHERE status = 'paid'
|> AGGREGATE region, sum(total) AS revenue GROUP BY region
|> ORDER BY revenue DESC
|> LIMIT 10;
```

Stages: `WHERE`, `FILTER`, `SELECT`, `AGGREGATE`, `ORDER BY`, `LIMIT`, `DISTINCT`, `JOIN`.

## Execution guards

The engine materializes source rows and executes joins/aggregations in memory. Default limits:

- 100,000 source rows per table
- 10,000 result rows
- 2,000,000 intermediate join rows
- 30 second query timeout

Use filters early. The scan planner performs capability-gated projection, filter and limit pushdown; the engine always re-evaluates filters, so connectors may ignore unsupported pushdown. Hash joins build the smaller side for inner joins. There is no cost-based join reordering yet.

## Known limitations

- No DML/DDL or transactions.
- No `PIVOT`/`UNPIVOT`, `DISTINCT ON`, `QUALIFY`, `TOP`, `LATERAL`, `UNNEST`, or named window definitions.
- `LIMIT`/`OFFSET` and window frame offsets accept integer literals, not bind parameters.
- No full interval/array/map/JSON type system; typed date/time values are represented as normalized strings.
- No cost-based join reordering; joins follow the written order and choose the smaller inner-join build side.
- Window functions are not available inside `|>` pipeline stages.
- Recursive CTEs require a single `UNION`/`UNION ALL` term and have the documented iteration/row guards.
- Set-operation type coercion is minimal; values are compared with AnyQuery's cross-source numeric normalization.

Unsupported constructs such as `DISTINCT ON`, `ARRAY[...]`, array subscripts and
`NATURAL CROSS JOIN` are rejected explicitly instead of being interpreted as a
nearby supported form.
