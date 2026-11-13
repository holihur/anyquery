package sql

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Statement is a parsed top-level statement.
type Statement interface {
	stmtNode()
}

// SelectStmt is a SELECT query. Pipe stages are applied after the base
// SELECT (or after FROM when using FROM-first pipeline syntax).
type SelectStmt struct {
	CTEs          []*CTE
	Distinct      bool
	Projections   []SelectItem
	From          []TableExpr
	Where         Expr
	GroupBy       []Expr
	GroupingSets  [][]Expr
	Having        Expr
	OrderBy       []OrderItem
	Limit         *int
	LimitExpr     Expr // set instead of Limit when the row count is an expression
	Offset        *int
	OffsetExpr    Expr // set instead of Offset when the offset is an expression
	FetchWithTies bool
	SetOps        []SetOperation
	Windows       []NamedWindow
	Pipes         []PipeStage
}

// NamedWindow is one WINDOW clause definition.
type NamedWindow struct {
	Name string
	Spec *WindowSpec
}

func (*SelectStmt) stmtNode() {}

// CTE is a named subquery. Materialized hints are intentionally omitted for now.
type CTE struct {
	Name      string
	Columns   []string
	Query     *SelectStmt
	Recursive bool
}

// SetOperation is a UNION / INTERSECT / EXCEPT term.
type SetOperation struct {
	Op    string // UNION, INTERSECT, EXCEPT
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
	Expr  Expr
	Desc  bool
	Nulls string // "", "first" or "last"
}

// TableExpr is a node in the FROM tree.
type TableExpr interface {
	tableNode()
}

// TableRef references source.table (table may contain dots after the source,
// e.g. pg.public.users -> Source=pg, Table=public.users).
type TableRef struct {
	Source   string
	Table    string
	Alias    string
	Temporal *TemporalClause
	Sample   *TableSample
}

// TemporalClause is a SQL:2016 FOR SYSTEM_TIME table selection.
type TemporalClause struct {
	Mode string // as_of, from_to, between, all
	AsOf Expr
	From Expr
	To   Expr
}

func (t *TemporalClause) String() string {
	if t == nil {
		return ""
	}
	switch t.Mode {
	case "as_of":
		return "FOR SYSTEM_TIME AS OF " + t.AsOf.String()
	case "from_to":
		return "FOR SYSTEM_TIME FROM " + t.From.String() + " TO " + t.To.String()
	case "between":
		return "FOR SYSTEM_TIME BETWEEN " + t.From.String() + " AND " + t.To.String()
	case "all":
		return "FOR SYSTEM_TIME ALL"
	}
	return ""
}

// TableSample describes TABLESAMPLE SYSTEM/BERNOULLI and REPEATABLE.
type TableSample struct {
	Method  string // SYSTEM or BERNOULLI
	Percent float64
	Seed    *int64
}

func (*TableRef) tableNode() {}

// DerivedTable is a subquery in FROM.
type DerivedTable struct {
	Query   *SelectStmt
	Alias   string
	Columns []string
}

func (*DerivedTable) tableNode() {}

// LateralTable marks a table expression as LATERAL; its subquery/function may
// reference columns from table expressions to its left.
type LateralTable struct {
	Table TableExpr
}

func (*LateralTable) tableNode() {}

// TableFunction is a FROM-clause table function such as UNNEST(expr).
type TableFunction struct {
	Name    string
	Args    []Expr
	Alias   string
	Columns []string
}

func (*TableFunction) tableNode() {}

// ValuesTable is a VALUES (...) table expression in FROM.
type ValuesTable struct {
	Rows    [][]Expr
	Alias   string
	Columns []string
}

func (*ValuesTable) tableNode() {}

// JSONTable is a SQL/JSON JSON_TABLE table expression.
type JSONTable struct {
	Expr    Expr
	Path    Expr
	Columns []JSONTableColumn
	Alias   string
}

func (*JSONTable) tableNode() {}

// JSONTableColumn is one column in JSON_TABLE's COLUMNS clause.
type JSONTableColumn struct {
	Name       string
	Type       string
	Path       Expr
	Ordinality bool
	NestedPath Expr
	Nested     []JSONTableColumn
}

// ValuesStmt is a top-level VALUES statement.
type ValuesStmt struct {
	Rows [][]Expr
}

func (*ValuesStmt) stmtNode() {}

// TableStmt is a top-level TABLE <table> statement.
type TableStmt struct {
	Ref *TableRef
}

func (*TableStmt) stmtNode() {}

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
	Left    TableExpr
	Right   TableExpr
	Type    JoinType
	On      Expr
	Using   []string
	Natural bool
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
	Right   TableExpr
	Type    JoinType
	On      Expr
	Using   []string
	Natural bool
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

// Parameter is a positional bind parameter. Index is 1-based for both ? and $n.
type Parameter struct {
	Index int
	Style string // ? or $
}

func (*Parameter) exprNode() {}

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

// QuantifiedExpr is = ANY/SOME/ALL (subquery or value list).
type QuantifiedExpr struct {
	Op       string
	Left     Expr
	List     []Expr
	Subquery *SelectStmt
	All      bool
}

func (*QuantifiedExpr) exprNode() {}

type BetweenExpr struct {
	Left    Expr
	Low     Expr
	High    Expr
	Negated bool
}

func (*BetweenExpr) exprNode() {}

type IsExpr struct {
	Left          Expr
	Right         Expr   // set for IS [NOT] DISTINCT FROM
	What          string // null, true, false, unknown, distinct, json
	Negated       bool
	JSONType      string // "", VALUE, SCALAR, ARRAY, OBJECT
	JSONUnique    bool
	JSONUniqueSet bool
}

func (*IsExpr) exprNode() {}

type FuncCall struct {
	Name        string
	Args        []Expr
	Star        bool
	Distinct    bool
	Filter      Expr
	WithinGroup []OrderItem
	Window      *WindowSpec

	// SQL/JSON function metadata.
	JSONPath         Expr
	JSONPairs        []JSONPair
	JSONOrder        []OrderItem
	JSONReturning    string
	JSONOnError      string
	JSONOnEmpty      string
	JSONWrapper      string
	JSONNullBehavior string // null or absent
	JSONUnique       bool
}

// JSONPair is one key/value pair in JSON_OBJECT / JSON_OBJECTAGG.
type JSONPair struct {
	Key   Expr
	Value Expr
}

// WindowSpec is an OVER (...) clause. FrameStart/FrameEnd use the values
// unbounded_preceding, current_row, unbounded_following, preceding, following;
// FrameOffset is meaningful for the last two.
type WindowSpec struct {
	Base             string // named window reference, e.g. OVER w
	PartitionBy      []Expr
	OrderBy          []OrderItem
	FrameKind        string // "", ROWS, RANGE or GROUPS
	FrameStart       string
	FrameStartOffset int
	FrameEnd         string
	FrameEndOffset   int
	FrameExclusion   string // "", CURRENT ROW, GROUP, TIES, NO OTHERS
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
	Safe bool // TRY_CAST returns NULL instead of an error
}

func (*CastExpr) exprNode() {}

// IntervalExpr is an INTERVAL literal such as INTERVAL '1 day' or
// INTERVAL '1' DAY.
type IntervalExpr struct {
	Value     string
	Qualifier string // e.g. DAY, YEAR TO MONTH
}

func (*IntervalExpr) exprNode() {}

func (i *IntervalExpr) String() string {
	out := "INTERVAL '" + strings.ReplaceAll(i.Value, "'", "''") + "'"
	if i.Qualifier != "" {
		out += " " + i.Qualifier
	}
	return out
}

type ExistsExpr struct{ Query *SelectStmt }

func (*ExistsExpr) exprNode() {}

type SubqueryExpr struct{ Query *SelectStmt }

func (*SubqueryExpr) exprNode() {}

// AtTimeZoneExpr is <expr> AT TIME ZONE <zone>.
type AtTimeZoneExpr struct {
	Expr Expr
	Zone Expr
}

func (*AtTimeZoneExpr) exprNode() {}

func (a *AtTimeZoneExpr) String() string {
	return "(" + a.Expr.String() + " AT TIME ZONE " + a.Zone.String() + ")"
}

// ---- formatting ------------------------------------------------------------

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// FormatIdent quotes an identifier when needed. It accepts the same
// identifier character set as the lexer (Unicode letters, digits, underscore
// and dollar), but an identifier may not start with a digit.
func FormatIdent(s string) string {
	if s == "" {
		return `""`
	}
	for i, r := range s {
		ok := r == '_' || r == '$' || unicode.IsLetter(r)
		if i > 0 {
			ok = ok || unicode.IsDigit(r)
		}
		if !ok {
			return quoteIdent(s)
		}
	}
	return s
}

// formatQualifiedName quotes each dot-separated part. The AST stores catalog
// and schema names joined with dots, so formatting must not quote the whole
// string as one identifier.
func formatQualifiedName(s string) string {
	parts := strings.Split(s, ".")
	for i, part := range parts {
		parts[i] = FormatIdent(part)
	}
	return strings.Join(parts, ".")
}

func FormatTableRef(t *TableRef) string {
	var out string
	if t.Source == "" {
		out = formatQualifiedName(t.Table)
	} else {
		parts := []string{FormatIdent(t.Source)}
		if t.Table != "" {
			parts = append(parts, formatQualifiedName(t.Table))
		}
		out = strings.Join(parts, ".")
	}
	if t.Temporal != nil {
		out += " " + t.Temporal.String()
	}
	return out
}

func (p *Parameter) String() string {
	if p.Style == "?" {
		return "?"
	}
	return "$" + fmt.Sprint(p.Index)
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
		return formatQualifiedName(s.Table) + ".*"
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

func (q *QuantifiedExpr) String() string {
	quant := "ANY"
	if q.All {
		quant = "ALL"
	}
	if q.Subquery != nil {
		return "(" + q.Left.String() + " " + q.Op + " " + quant + " (" + FormatSelect(q.Subquery) + "))"
	}
	parts := make([]string, len(q.List))
	for i, e := range q.List {
		parts[i] = e.String()
	}
	return "(" + q.Left.String() + " " + q.Op + " " + quant + " (" + strings.Join(parts, ", ") + "))"
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
	if i.What == "distinct" {
		return "(" + i.Left.String() + op + "DISTINCT FROM " + i.Right.String() + ")"
	}
	if i.What == "json" {
		out := "(" + i.Left.String() + op + "JSON"
		if i.JSONType != "" {
			out += " " + i.JSONType
		}
		if i.JSONUniqueSet {
			if i.JSONUnique {
				out += " WITH UNIQUE KEYS"
			} else {
				out += " WITHOUT UNIQUE KEYS"
			}
		}
		return out + ")"
	}
	return "(" + i.Left.String() + op + strings.ToUpper(i.What) + ")"
}

func (f *FuncCall) String() string {
	if out := formatJSONFunc(f); out != "" {
		return out
	}
	if strings.EqualFold(f.Name, "overlay") && (len(f.Args) == 3 || len(f.Args) == 4) {
		out := "overlay(" + f.Args[0].String() + " PLACING " + f.Args[1].String() + " FROM " + f.Args[2].String()
		if len(f.Args) == 4 {
			out += " FOR " + f.Args[3].String()
		}
		return out + ")"
	}
	if strings.EqualFold(f.Name, "extract") && len(f.Args) == 2 {
		part := f.Args[0].String()
		if lit, ok := f.Args[0].(*Literal); ok && lit.Kind == LitString {
			part = fmt.Sprint(lit.Value)
		}
		return "extract(" + part + " FROM " + f.Args[1].String() + ")"
	}
	if strings.EqualFold(f.Name, "position") && len(f.Args) == 2 {
		return "position(" + f.Args[0].String() + " IN " + f.Args[1].String() + ")"
	}
	var out string
	if f.Star {
		out = f.Name + "(*)"
	} else {
		args := make([]string, len(f.Args))
		for i, a := range f.Args {
			args[i] = a.String()
		}
		prefix := ""
		if f.Distinct {
			prefix = "DISTINCT "
		}
		out = f.Name + "(" + prefix + strings.Join(args, ", ") + ")"
	}
	if len(f.WithinGroup) > 0 {
		out += " WITHIN GROUP (ORDER BY " + formatOrder(f.WithinGroup) + ")"
	}
	if f.Filter != nil {
		out += " FILTER (WHERE " + f.Filter.String() + ")"
	}
	if f.Window != nil {
		out += " OVER (" + f.Window.String() + ")"
	}
	return out
}

func (w *WindowSpec) String() string {
	var parts []string
	if w.Base != "" {
		parts = append(parts, FormatIdent(w.Base))
	}
	if len(w.PartitionBy) > 0 {
		exprs := make([]string, len(w.PartitionBy))
		for i, e := range w.PartitionBy {
			exprs[i] = e.String()
		}
		parts = append(parts, "PARTITION BY "+strings.Join(exprs, ", "))
	}
	if len(w.OrderBy) > 0 {
		parts = append(parts, "ORDER BY "+formatOrder(w.OrderBy))
	}
	if w.FrameKind != "" {
		frame := w.FrameKind
		if w.FrameEnd != "" {
			frame += " BETWEEN " + formatFrameBound(w.FrameStart, w.FrameStartOffset) + " AND " + formatFrameBound(w.FrameEnd, w.FrameEndOffset)
		} else {
			frame += " " + formatFrameBound(w.FrameStart, w.FrameStartOffset)
		}
		if w.FrameExclusion != "" {
			frame += " EXCLUDE " + strings.ToUpper(w.FrameExclusion)
		}
		parts = append(parts, frame)
	}
	return strings.Join(parts, " ")
}

func formatFrameBound(kind string, offset int) string {
	switch kind {
	case "unbounded_preceding":
		return "UNBOUNDED PRECEDING"
	case "current_row":
		return "CURRENT ROW"
	case "unbounded_following":
		return "UNBOUNDED FOLLOWING"
	case "preceding":
		return fmt.Sprintf("%d PRECEDING", offset)
	case "following":
		return fmt.Sprintf("%d FOLLOWING", offset)
	}
	return kind
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
	name := "CAST"
	if c.Safe {
		name = "TRY_CAST"
	}
	return name + "(" + c.Expr.String() + " AS " + c.Type + ")"
}

func (e *ExistsExpr) String() string { return "EXISTS(" + FormatSelect(e.Query) + ")" }

func (s *SubqueryExpr) String() string { return "(" + FormatSelect(s.Query) + ")" }

func formatJSONFunc(f *FuncCall) string {
	name := strings.ToLower(f.Name)
	var out string
	switch name {
	case "json_value", "json_query", "json_exists":
		if len(f.Args) != 1 || f.JSONPath == nil {
			return ""
		}
		out = name + "(" + f.Args[0].String() + ", " + f.JSONPath.String()
	case "json_object":
		parts := make([]string, len(f.JSONPairs))
		for i, p := range f.JSONPairs {
			parts[i] = p.Key.String() + ": " + p.Value.String()
		}
		out = "json_object(" + strings.Join(parts, ", ")
	case "json_array":
		parts := make([]string, len(f.Args))
		for i, a := range f.Args {
			parts[i] = a.String()
		}
		out = "json_array(" + strings.Join(parts, ", ")
	case "json_arrayagg":
		if len(f.Args) != 1 {
			return ""
		}
		out = "json_arrayagg(" + f.Args[0].String()
		if len(f.JSONOrder) > 0 {
			out += " ORDER BY " + formatOrder(f.JSONOrder)
		}
	case "json_objectagg":
		if len(f.JSONPairs) != 1 {
			return ""
		}
		out = "json_objectagg(" + f.JSONPairs[0].Key.String() + ": " + f.JSONPairs[0].Value.String()
		if len(f.JSONOrder) > 0 {
			out += " ORDER BY " + formatOrder(f.JSONOrder)
		}
	default:
		return ""
	}
	if f.JSONReturning != "" {
		out += " RETURNING " + f.JSONReturning
	}
	if f.JSONUnique {
		out += " WITH UNIQUE KEYS"
	}
	if f.JSONNullBehavior == "null" {
		out += " NULL ON NULL"
	} else if f.JSONNullBehavior == "absent" {
		out += " ABSENT ON NULL"
	}
	return out + ")"
}

func FormatSelectItem(i SelectItem) string {
	var s string
	if i.Star {
		if i.StarTable != "" {
			s = formatQualifiedName(i.StarTable) + ".*"
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
		recursive := false
		for _, c := range s.CTEs {
			if c.Recursive {
				recursive = true
			}
		}
		sb.WriteString("WITH ")
		if recursive {
			sb.WriteString("RECURSIVE ")
		}
		for i, c := range s.CTEs {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(FormatIdent(c.Name))
			if len(c.Columns) > 0 {
				cols := make([]string, len(c.Columns))
				for j, col := range c.Columns {
					cols[j] = FormatIdent(col)
				}
				sb.WriteString("(" + strings.Join(cols, ", ") + ")")
			}
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
	if len(s.GroupingSets) > 0 {
		sb.WriteString(" GROUP BY GROUPING SETS (")
		for i, set := range s.GroupingSets {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			parts := make([]string, len(set))
			for j, g := range set {
				parts[j] = g.String()
			}
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteString(")")
		}
		sb.WriteString(")")
	} else if len(s.GroupBy) > 0 {
		parts := make([]string, len(s.GroupBy))
		for i, g := range s.GroupBy {
			parts[i] = g.String()
		}
		sb.WriteString(" GROUP BY " + strings.Join(parts, ", "))
	}
	if s.Having != nil {
		sb.WriteString(" HAVING " + s.Having.String())
	}
	if len(s.Windows) > 0 {
		sb.WriteString(" WINDOW ")
		for i, w := range s.Windows {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(FormatIdent(w.Name) + " AS (" + w.Spec.String() + ")")
		}
	}
	for _, op := range s.SetOps {
		sb.WriteString(" " + strings.ToUpper(op.Op))
		if op.All {
			sb.WriteString(" ALL")
		}
		sb.WriteString(" ")
		sb.WriteString(FormatSelect(op.Right))
	}
	if len(s.OrderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(formatOrder(s.OrderBy))
	}
	if s.FetchWithTies && (s.Limit != nil || s.LimitExpr != nil) {
		if s.Offset != nil || s.OffsetExpr != nil {
			sb.WriteString(" OFFSET " + formatRowCount(s.Offset, s.OffsetExpr))
		}
		sb.WriteString(" FETCH FIRST " + formatRowCount(s.Limit, s.LimitExpr) + " ROWS WITH TIES")
	} else {
		if s.Limit != nil || s.LimitExpr != nil {
			sb.WriteString(" LIMIT " + formatRowCount(s.Limit, s.LimitExpr))
		}
		if s.Offset != nil || s.OffsetExpr != nil {
			sb.WriteString(" OFFSET " + formatRowCount(s.Offset, s.OffsetExpr))
		}
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
		if o.Nulls != "" {
			parts[i] += " NULLS " + strings.ToUpper(o.Nulls)
		}
	}
	return strings.Join(parts, ", ")
}

func FormatTableExpr(t TableExpr) string {
	switch x := t.(type) {
	case *TableRef:
		s := FormatTableRef(x)
		if x.Sample != nil {
			s += " " + FormatTableSample(x.Sample)
		}
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		return s
	case *LateralTable:
		return "LATERAL " + FormatTableExpr(x.Table)
	case *DerivedTable:
		s := "(" + FormatSelect(x.Query) + ")"
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		if len(x.Columns) > 0 {
			cols := make([]string, len(x.Columns))
			for i, col := range x.Columns {
				cols[i] = FormatIdent(col)
			}
			s += "(" + strings.Join(cols, ", ") + ")"
		}
		return s
	case *TableFunction:
		args := make([]string, len(x.Args))
		for i, arg := range x.Args {
			args[i] = arg.String()
		}
		s := FormatIdent(x.Name) + "(" + strings.Join(args, ", ") + ")"
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		if len(x.Columns) > 0 {
			cols := make([]string, len(x.Columns))
			for i, col := range x.Columns {
				cols[i] = FormatIdent(col)
			}
			s += "(" + strings.Join(cols, ", ") + ")"
		}
		return s
	case *ValuesTable:
		rows := make([]string, len(x.Rows))
		for i, row := range x.Rows {
			vals := make([]string, len(row))
			for j, e := range row {
				vals[j] = e.String()
			}
			rows[i] = "(" + strings.Join(vals, ", ") + ")"
		}
		s := "(VALUES " + strings.Join(rows, ", ") + ")"
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		if len(x.Columns) > 0 {
			cols := make([]string, len(x.Columns))
			for i, col := range x.Columns {
				cols[i] = FormatIdent(col)
			}
			s += "(" + strings.Join(cols, ", ") + ")"
		}
		return s
	case *JSONTable:
		s := "JSON_TABLE(" + x.Expr.String() + ", " + x.Path.String() + " COLUMNS (" + formatJSONTableColumns(x.Columns) + "))"
		if x.Alias != "" {
			s += " AS " + FormatIdent(x.Alias)
		}
		return s
	case *JoinExpr:
		prefix := ""
		if x.Natural {
			prefix = "NATURAL "
		}
		s := FormatTableExpr(x.Left) + " " + prefix + strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right)
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

func formatRowCount(literal *int, expr Expr) string {
	if literal != nil {
		return fmt.Sprint(*literal)
	}
	if expr != nil {
		return expr.String()
	}
	return "ALL"
}

// FormatTableSample formats a TABLESAMPLE clause.
func FormatTableSample(s *TableSample) string {
	if s == nil {
		return ""
	}
	out := "TABLESAMPLE " + strings.ToUpper(s.Method) + " (" + strconv.FormatFloat(s.Percent, 'f', -1, 64) + ")"
	if s.Seed != nil {
		out += " REPEATABLE (" + fmt.Sprint(*s.Seed) + ")"
	}
	return out
}

// FormatValues formats a top-level VALUES statement.
func FormatValues(v *ValuesStmt) string {
	rows := make([]string, len(v.Rows))
	for i, row := range v.Rows {
		vals := make([]string, len(row))
		for j, e := range row {
			vals[j] = e.String()
		}
		rows[i] = "(" + strings.Join(vals, ", ") + ")"
	}
	return "VALUES " + strings.Join(rows, ", ")
}

// FormatTable formats a top-level TABLE statement.
func FormatTable(t *TableStmt) string {
	if t == nil || t.Ref == nil {
		return "TABLE"
	}
	return "TABLE " + FormatTableRef(t.Ref)
}

// FormatStatement formats a parsed top-level statement when a formatter is
// defined. It returns an empty string for statements without a formatter.
func FormatStatement(stmt Statement) string {
	switch s := stmt.(type) {
	case *SelectStmt:
		return FormatSelect(s)
	case *ValuesStmt:
		return FormatValues(s)
	case *TableStmt:
		return FormatTable(s)
	}
	return ""
}

func formatJSONTableColumns(cols []JSONTableColumn) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.NestedPath != nil {
			parts = append(parts, "NESTED PATH "+c.NestedPath.String()+" COLUMNS ("+formatJSONTableColumns(c.Nested)+")")
			continue
		}
		p := FormatIdent(c.Name)
		if c.Ordinality {
			p += " FOR ORDINALITY"
		} else {
			p += " " + c.Type
			if c.Path != nil {
				p += " PATH " + c.Path.String()
			}
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
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
		prefix := ""
		if x.Natural {
			prefix = "NATURAL "
		}
		s := prefix + strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right)
		if x.On != nil {
			s += " ON " + x.On.String()
		}
		if len(x.Using) > 0 {
			s = prefix + strings.ToUpper(string(x.Type)) + " JOIN " + FormatTableExpr(x.Right) + " USING (" + strings.Join(x.Using, ", ") + ")"
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
	case *QuantifiedExpr:
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
		if !WalkExpr(x.Left, fn) {
			return false
		}
		if x.Right != nil {
			return WalkExpr(x.Right, fn)
		}
	case *FuncCall:
		for _, a := range x.Args {
			if !WalkExpr(a, fn) {
				return false
			}
		}
		if x.Filter != nil && !WalkExpr(x.Filter, fn) {
			return false
		}
		for _, o := range x.WithinGroup {
			if !WalkExpr(o.Expr, fn) {
				return false
			}
		}
		for _, o := range x.JSONOrder {
			if !WalkExpr(o.Expr, fn) {
				return false
			}
		}
		for _, p := range x.JSONPairs {
			if !WalkExpr(p.Key, fn) || !WalkExpr(p.Value, fn) {
				return false
			}
		}
		if x.JSONPath != nil && !WalkExpr(x.JSONPath, fn) {
			return false
		}
		if x.Window != nil {
			for _, e := range x.Window.PartitionBy {
				if !WalkExpr(e, fn) {
					return false
				}
			}
			for _, o := range x.Window.OrderBy {
				if !WalkExpr(o.Expr, fn) {
					return false
				}
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

// ContainsWindow reports whether an expression contains a window function.
func ContainsWindow(e Expr) bool {
	found := false
	WalkExpr(e, func(x Expr) bool {
		if f, ok := x.(*FuncCall); ok && f.Window != nil {
			found = true
			return false
		}
		return true
	})
	return found
}

// ContainsAggregate reports whether an expression contains an aggregate call.
func ContainsAggregate(e Expr) bool {
	found := false
	WalkExpr(e, func(x Expr) bool {
		if f, ok := x.(*FuncCall); ok && f.Window == nil && IsAggregate(f.Name) {
			found = true
			return false
		}
		return true
	})
	return found
}

// IsWindow reports whether f is a window function call.
func IsWindow(f *FuncCall) bool { return f != nil && f.Window != nil }

// IsAggregate reports whether name is an aggregate function.
func IsAggregate(name string) bool {
	switch strings.ToLower(name) {
	case "count", "sum", "avg", "min", "max", "stddev", "stddev_pop", "stddev_samp", "variance", "var_pop", "var_samp", "group_concat", "string_agg", "array_agg", "bool_and", "bool_or", "percentile_cont", "percentile_disc", "mode", "json_arrayagg", "json_objectagg":
		return true
	}
	return false
}
