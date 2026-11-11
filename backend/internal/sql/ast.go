// Package sql contains AnyQuery's own SQL parser and AST.
//
// The grammar is intentionally owned (not generated from a third-party MySQL
// parser) so the platform can grow new dialect features, pipe stages and
// source-specific extensions without waiting for an upstream parser.
package sql

import (
	"fmt"
	"strings"
)

// Statement is a parsed top-level statement.
type Statement interface {
	stmtNode()
}

// SelectStmt is a SELECT query. Pipe stages are applied after the base
// SELECT (or after FROM when using FROM-first pipeline syntax).
type SelectStmt struct {
	CTEs        []*CTE
	Distinct    bool
	Projections []SelectItem
	From        []TableExpr
	Where       Expr
	GroupBy     []Expr
	Having      Expr
	OrderBy     []OrderItem
	Limit       *int
	Offset      *int
	Pipes       []PipeStage
}

func (*SelectStmt) stmtNode() {}

// CTE is a named subquery. Materialized hints are intentionally omitted for now.
type CTE struct {
	Name    string
	Columns []string
	Query   *SelectStmt
}

// SetOperation is reserved for UNION support.
type SetOperation struct {
	All   bool
	Right *SelectStmt
}

// ShowStmt implements SHOW TABLES / COLUMNS / SOURCES.
type ShowStmt struct {
	Target string // tables, columns, sources
	Source string
	Table  string
}

func (*ShowStmt) stmtNode() {}

// DescribeStmt implements DESCRIBE <source>.<table>.
type DescribeStmt struct {
	Source string
	Table  string
}

func (*DescribeStmt) stmtNode() {}

// ExplainStmt wraps another statement.
type ExplainStmt struct {
	Stmt Statement
}

func (*ExplainStmt) stmtNode() {}

// SelectItem is one projection.
type SelectItem struct {
	Star      bool
	StarTable string // optional alias or table qualifier for star
	Expr      Expr
	Alias     string
}

// OrderItem is one ORDER BY term.
type OrderItem struct {
	Expr Expr
	Desc bool
}

// TableExpr is a node in the FROM tree.
type TableExpr interface {
	tableNode()
}

// TableRef references source.table (table may contain dots after the source,
// e.g. pg.public.users -> Source=pg, Table=public.users).
type TableRef struct {
	Source string
	Table  string
	Alias  string
}

func (*TableRef) tableNode() {}

// DerivedTable is a subquery in FROM.
type DerivedTable struct {
	Query *SelectStmt
	Alias string
}

func (*DerivedTable) tableNode() {}

// JoinType enumerates supported joins.
type JoinType string

const (
	JoinInner JoinType = "inner"
	JoinLeft  JoinType = "left"
	JoinRight JoinType = "right"
	JoinFull  JoinType = "full"
	JoinCross JoinType = "cross"
)

// JoinExpr joins two table expressions.
type JoinExpr struct {
	Left  TableExpr
	Right TableExpr
	Type  JoinType
	On    Expr
	Using []string
}

func (*JoinExpr) tableNode() {}

// PipeStage is one stage of a pipeline.
type PipeStage interface {
	pipeNode()
}

type PipeWhere struct{ Expr Expr }

func (*PipeWhere) pipeNode() {}

type PipeSelect struct {
	Items    []SelectItem
	Distinct bool
}

func (*PipeSelect) pipeNode() {}

type PipeAggregate struct {
	Items   []SelectItem
	GroupBy []Expr
}

func (*PipeAggregate) pipeNode() {}

type PipeOrder struct{ Items []OrderItem }

func (*PipeOrder) pipeNode() {}

type PipeLimit struct {
	Limit  int
	Offset int
}

func (*PipeLimit) pipeNode() {}

type PipeJoin struct {
	Right TableExpr
	Type  JoinType
	On    Expr
	Using []string
}

func (*PipeJoin) pipeNode() {}

type PipeDistinct struct{}

func (*PipeDistinct) pipeNode() {}

// ---- expressions -----------------------------------------------------------

// Expr is an expression node.
type Expr interface {
	exprNode()
	String() string
}

type Literal struct {
	Kind  LiteralKind
	Value any
}

type LiteralKind string

const (
	LitString LiteralKind = "string"
	LitInt    LiteralKind = "int"
	LitFloat  LiteralKind = "float"
	LitBool   LiteralKind = "bool"
	LitNull   LiteralKind = "null"
)

func (*Literal) exprNode() {}

type ColumnRef struct {
	Parts []string // 1: col, 2: table.col, 3: source.table.col
}

func (*ColumnRef) exprNode() {}

type StarExpr struct{ Table string }

func (*StarExpr) exprNode() {}

type UnaryExpr struct {
	Op   string
	Expr Expr
}

func (*UnaryExpr) exprNode() {}

type BinaryExpr struct {
	Op          string
	Left, Right Expr
}

func (*BinaryExpr) exprNode() {}

// CompareExpr covers =, !=, <, <=, >, >=, LIKE, ILIKE, REGEXP.
type CompareExpr struct {
	Op      string
	Left    Expr
	Right   Expr
	Negated bool
}

func (*CompareExpr) exprNode() {}

type InExpr struct {
	Left     Expr
	List     []Expr
	Subquery *SelectStmt
	Negated  bool
}

func (*InExpr) exprNode() {}

type BetweenExpr struct {
	Left    Expr
	Low     Expr
	High    Expr
	Negated bool
}

func (*BetweenExpr) exprNode() {}

type IsExpr struct {
	Left    Expr
	What    string // null, true, false, unknown
	Negated bool
}

func (*IsExpr) exprNode() {}

type FuncCall struct {
	Name     string
	Args     []Expr
	Star     bool
	Distinct bool
}

func (*FuncCall) exprNode() {}

type CaseExpr struct {
	Base  Expr
	Whens []WhenClause
	Else  Expr
}

type WhenClause struct {
	Cond Expr
	Then Expr
}

func (*CaseExpr) exprNode() {}

type CastExpr struct {
	Expr Expr
	Type string
}

func (*CastExpr) exprNode() {}

type ExistsExpr struct{ Query *SelectStmt }

func (*ExistsExpr) exprNode() {}

type SubqueryExpr struct{ Query *SelectStmt }

func (*SubqueryExpr) exprNode() {}

// ---- formatting ------------------------------------------------------------

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func FormatIdent(s string) string {
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return quoteIdent(s)
		}
	}
	if s == "" {
		return `""`
	}
	return s
}

func FormatTableRef(t *TableRef) string {
	parts := []string{t.Source}
	if t.Table != "" {
		parts = append(parts, FormatIdent(t.Table))
	}
	return strings.Join(parts, ".")
}

func (l *Literal) String() string {
	switch l.Kind {
	case LitNull:
		return "NULL"
	case LitBool:
		if b, _ := l.Value.(bool); b {
			return "TRUE"
		}
		return "FALSE"
	case LitString:
		return "'" + strings.ReplaceAll(fmt.Sprint(l.Value), "'", "''") + "'"
	default:
		return fmt.Sprint(l.Value)
	}
}

func (c *ColumnRef) String() string {
	parts := make([]string, len(c.Parts))
	for i, p := range c.Parts {
		parts[i] = FormatIdent(p)
	}
	return strings.Join(parts, ".")
}

func (s *StarExpr) String() string {
	if s.Table != "" {
		return FormatIdent(s.Table) + ".*"
	}
	return "*"
}

func (u *UnaryExpr) String() string { return u.Op + " " + u.Expr.String() }

func (b *BinaryExpr) String() string {
	return "(" + b.Left.String() + " " + b.Op + " " + b.Right.String() + ")"
}

func (c *CompareExpr) String() string {
	op := c.Op
	if c.Negated {
		op = "NOT " + op
	}
	return "(" + c.Left.String() + " " + op + " " + c.Right.String() + ")"
}

func (i *InExpr) String() string {
	op := " IN "
	if i.Negated {
		op = " NOT IN "
	}
	if i.Subquery != nil {
		return "(" + i.Left.String() + op + "(" + FormatSelect(i.Subquery) + "))"
	}
	parts := make([]string, len(i.List))
	for n, e := range i.List {
		parts[n] = e.String()
	}
	return "(" + i.Left.String() + op + "(" + strings.Join(parts, ", ") + "))"
}

func (b *BetweenExpr) String() string {
	op := " BETWEEN "
	if b.Negated {
		op = " NOT BETWEEN "
	}
	return "(" + b.Left.String() + op + b.Low.String() + " AND " + b.High.String() + ")"
}

func (i *IsExpr) String() string {
	op := " IS "
	if i.Negated {
		op = " IS NOT "
	}
	return "(" + i.Left.String() + op + strings.ToUpper(i.What) + ")"
}

func (f *FuncCall) String() string {
	if f.Star {
		return f.Name + "(*)"
	}
	args := make([]string, len(f.Args))
	for i, a := range f.Args {
		args[i] = a.String()
	}
	prefix := ""
	if f.Distinct {
		prefix = "DISTINCT "
	}
	return f.Name + "(" + prefix + strings.Join(args, ", ") + ")"
}

func (c *CaseExpr) String() string {
	var sb strings.Builder
	sb.WriteString("CASE")
	if c.Base != nil {
		sb.WriteString(" " + c.Base.String())
	}
	for _, w := range c.Whens {
		sb.WriteString(" WHEN " + w.Cond.String() + " THEN " + w.Then.String())
	}
	if c.Else != nil {
		sb.WriteString(" ELSE " + c.Else.String())
	}
	sb.WriteString(" END")
	return sb.String()
}

func (c *CastExpr) String() string {
	return "CAST(" + c.Expr.String() + " AS " + c.Type + ")"
}

func (e *ExistsExpr) String() string { return "EXISTS(" + FormatSelect(e.Query) + ")" }

func (s *SubqueryExpr) String() string { return "(" + FormatSelect(s.Query) + ")" }

func FormatSelectItem(i SelectItem) string {
	var s string
	if i.Star {
		if i.StarTable != "" {
			s = FormatIdent(i.StarTable) + ".*"
		} else {
			s = "*"
		}
	} else {
		s = i.Expr.String()
	}
	if i.Alias != "" {
		s += " AS " + FormatIdent(i.Alias)
	}
	return s
}

func FormatSelect(s *SelectStmt) string {
	var sb strings.Builder
	if len(s.CTEs) > 0 {
		sb.WriteString("WITH ")
		for i, c := range s.CTEs {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(FormatIdent(c.Name))
			sb.WriteString(" AS (")
			sb.WriteString(FormatSelect(c.Query))
			sb.WriteString(")")
		}
		sb.WriteString(" ")
	}
	sb.WriteString("SELECT ")
	if s.Distinct {
		sb.WriteString("DISTINCT ")
	}
	items := make([]string, len(s.Projections))
	for i, p := range s.Projections {
		items[i] = FormatSelectItem(p)
	}
	if len(items) == 0 {
		sb.WriteString("*")
	} else {
		sb.WriteString(strings.Join(items, ", "))
	}
	if len(s.From) > 0 {
		sb.WriteString(" FROM ")
		parts := make([]string, len(s.From))
		for i, f := range s.From {
			parts[i] = FormatTableExpr(f)
		}
		sb.WriteString(strings.Join(parts, ", "))
	}
	if s.Where != nil {
		sb.WriteString(" WHERE " + s.Where.String())
	}
	if len(s.GroupBy) > 0 {
		parts := make([]string, len(s.GroupBy))
		for i, g := range s.GroupBy {
			parts[i] = g.String()
		}
		sb.WriteString(" GROUP BY " + strings.Join(parts, ", "))
	}
	if s.Having != nil {
		sb.WriteString(" HAVING " + s.Having.String())
	}
	if len(s.OrderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(formatOrder(s.OrderBy))
	}
	if s.Limit != nil {
		sb.WriteString(" LIMIT " + fmt.Sprint(*s.Limit))
	}
	if s.Offset != nil {
		sb.WriteString(" OFFSET " + fmt.Sprint(*s.Offset))
	}
	for _, p := range s.Pipes {
		sb.WriteString(" |> ")
		sb.WriteString(FormatPipe(p))
	}
	return sb.String()
}

func formatOrder(items []OrderItem) string {
	parts := make([]string, len(items))
	for i, o := range items {
		parts[i] = o.Expr.String()
		if o.Desc {
			parts[i] += " DESC"
		}
	}
	return strings.Join(parts, ", ")
}

func FormatTableExpr(t TableExpr) string {
	switch x := t.(type) {
	case *TableRef:
		s := FormatTableRef(x)
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		return s
	case *DerivedTable:
		s := "(" + FormatSelect(x.Query) + ")"
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		return s
	case *JoinExpr:
		s := FormatTableExpr(x.Left) + " " + strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right)
		if x.On != nil {
			s += " ON " + x.On.String()
		}
		if len(x.Using) > 0 {
			s += " USING (" + strings.Join(x.Using, ", ") + ")"
		}
		return s
	}
	return ""
}

func FormatPipe(p PipeStage) string {
	switch x := p.(type) {
	case *PipeWhere:
		return "WHERE " + x.Expr.String()
	case *PipeSelect:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = FormatSelectItem(it)
		}
		prefix := ""
		if x.Distinct {
			prefix = "DISTINCT "
		}
		return "SELECT " + prefix + strings.Join(parts, ", ")
	case *PipeAggregate:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = FormatSelectItem(it)
		}
		s := "AGGREGATE " + strings.Join(parts, ", ")
		if len(x.GroupBy) > 0 {
			groups := make([]string, len(x.GroupBy))
			for i, g := range x.GroupBy {
				groups[i] = g.String()
			}
			s += " GROUP BY " + strings.Join(groups, ", ")
		}
		return s
	case *PipeOrder:
		return "ORDER BY " + formatOrder(x.Items)
	case *PipeLimit:
		if x.Offset > 0 {
			return fmt.Sprintf("LIMIT %d OFFSET %d", x.Limit, x.Offset)
		}
		return fmt.Sprintf("LIMIT %d", x.Limit)
	case *PipeJoin:
		s := strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right)
		if x.On != nil {
			s += " ON " + x.On.String()
		}
		if len(x.Using) > 0 {
			s = strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right) + " USING (" + strings.Join(x.Using, ", ") + ")"
		}
		return s
	case *PipeDistinct:
		return "DISTINCT"
	}
	return ""
}

// Label returns a human-readable output name for a select item.
func Label(item SelectItem) string {
	if item.Alias != "" {
		return item.Alias
	}
	if item.Star {
		return "*"
	}
	if c, ok := item.Expr.(*ColumnRef); ok && len(c.Parts) > 0 {
		return c.Parts[len(c.Parts)-1]
	}
	if f, ok := item.Expr.(*FuncCall); ok {
		return strings.ToLower(f.Name)
	}
	return item.Expr.String()
}

// WalkExpr visits every expression node bottom-up. Returning false stops the
// walk. It is the extension point used by column resolution and catalogs.
func WalkExpr(e Expr, fn func(Expr) bool) bool {
	if e == nil {
		return true
	}
	if !fn(e) {
		return false
	}
	switch x := e.(type) {
	case *UnaryExpr:
		return WalkExpr(x.Expr, fn)
	case *BinaryExpr:
		return WalkExpr(x.Left, fn) && WalkExpr(x.Right, fn)
	case *CompareExpr:
		return WalkExpr(x.Left, fn) && WalkExpr(x.Right, fn)
	case *InExpr:
		if !WalkExpr(x.Left, fn) {
			return false
		}
		for _, e := range x.List {
			if !WalkExpr(e, fn) {
				return false
			}
		}
	case *BetweenExpr:
		return WalkExpr(x.Left, fn) && WalkExpr(x.Low, fn) && WalkExpr(x.High, fn)
	case *IsExpr:
		return WalkExpr(x.Left, fn)
	case *FuncCall:
		for _, a := range x.Args {
			if !WalkExpr(a, fn) {
				return false
			}
		}
	case *CaseExpr:
		if x.Base != nil && !WalkExpr(x.Base, fn) {
			return false
		}
		for _, w := range x.Whens {
			if !WalkExpr(w.Cond, fn) || !WalkExpr(w.Then, fn) {
				return false
			}
		}
		if x.Else != nil {
			return WalkExpr(x.Else, fn)
		}
	case *CastExpr:
		return WalkExpr(x.Expr, fn)
	}
	return true
}

// ContainsAggregate reports whether an expression contains an aggregate call.
func ContainsAggregate(e Expr) bool {
	found := false
	WalkExpr(e, func(x Expr) bool {
		if f, ok := x.(*FuncCall); ok && IsAggregate(f.Name) {
			found = true
			return false
		}
		return true
	})
	return found
}

// IsAggregate reports whether name is an aggregate function.
func IsAggregate(name string) bool {
	switch strings.ToLower(name) {
	case "count", "sum", "avg", "min", "max", "stddev", "stddev_pop", "stddev_samp", "variance", "var_pop", "var_samp", "group_concat", "string_agg", "array_agg", "bool_and", "bool_or":
		return true
	}
	return false
}
