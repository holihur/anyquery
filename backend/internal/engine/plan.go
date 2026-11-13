package engine

import (
	"context"
	"strings"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/sql"
)

// PlanNode is a structured logical/physical execution plan. It is intentionally
// closer to the operators the engine actually runs than to the SQL AST.
type PlanNode struct {
	ID            string         `json:"id"`
	Operator      string         `json:"operator"`
	Detail        string         `json:"detail,omitempty"`
	EstimatedRows int64          `json:"estimatedRows,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
	Children      []*PlanNode    `json:"children,omitempty"`
}

type planBuilder struct {
	ec   *execCtx
	next int
}

// Explain parses a query and returns a visualizable plan without reading source
// rows. Catalog metadata and a short-lived connector handle are used to compute
// pushdown hints and render source-side statements.
func (e *Engine) Explain(ctx context.Context, query string, params []any, cat Catalog) (*PlanNode, error) {
	if e.Opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Opts.Timeout)
		defer cancel()
	}
	stmt, err := sql.Parse(query)
	if err != nil {
		return nil, &Error{Code: "parse_error", Message: err.Error()}
	}
	ec := &execCtx{
		ctx:     ctx,
		engine:  e,
		catalog: cat,
		opened:  map[string]connector.Connector{},
		ctes:    map[string]*dataset{},
		params:  params,
	}
	defer ec.close()
	pb := &planBuilder{ec: ec}
	switch s := stmt.(type) {
	case *sql.SelectStmt:
		return pb.selectPlan(s), nil
	case *sql.ExplainStmt:
		if inner, ok := s.Stmt.(*sql.SelectStmt); ok {
			return pb.selectPlan(inner), nil
		}
		return &PlanNode{ID: pb.id(), Operator: "Statement", Detail: "EXPLAIN"}, nil
	case *sql.ShowStmt:
		return &PlanNode{ID: pb.id(), Operator: "Show", Detail: s.Target}, nil
	case *sql.DescribeStmt:
		return &PlanNode{ID: pb.id(), Operator: "Describe", Detail: s.Source + "." + s.Table}, nil
	default:
		return nil, errf("unsupported", "EXPLAIN does not support %T", stmt)
	}
}

func (pb *planBuilder) id() string {
	pb.next++
	return "n" + itoa(pb.next)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (pb *planBuilder) selectPlan(s *sql.SelectStmt) *PlanNode {
	hints := pb.ec.buildScanHints(s)
	node := pb.fromPlan(s.From, hints)
	if s.Where != nil {
		node = pb.wrap("Filter", s.Where.String(), node, map[string]any{"predicate": s.Where.String()})
	}
	hasAgg := len(s.GroupBy) > 0 || len(s.GroupingSets) > 0
	for _, item := range s.Projections {
		if item.Expr != nil && sql.ContainsAggregate(item.Expr) {
			hasAgg = true
		}
	}
	if s.Having != nil && sql.ContainsAggregate(s.Having) {
		hasAgg = true
	}
	if hasAgg {
		props := map[string]any{}
		if len(s.GroupBy) > 0 {
			props["groupBy"] = exprStrings(s.GroupBy)
		}
		if len(s.GroupingSets) > 0 {
			var sets []string
			for _, set := range s.GroupingSets {
				sets = append(sets, "("+strings.Join(exprStrings(set), ", ")+")")
			}
			props["groupingSets"] = sets
		}
		if aggs := aggregateNames(s); len(aggs) > 0 {
			props["aggregates"] = aggs
		}
		node = pb.wrap("Aggregate", summarizeGrouping(s), node, props)
	}
	if s.Having != nil {
		node = pb.wrap("Having", s.Having.String(), node, nil)
	}
	if windows := windowNames(s); len(windows) > 0 {
		node = pb.wrap("Window", strings.Join(windows, ", "), node, map[string]any{"functions": windows})
	}
	if len(s.Projections) > 0 {
		var labels []string
		for _, item := range s.Projections {
			labels = append(labels, sql.Label(item))
		}
		node = pb.wrap("Project", strings.Join(labels, ", "), node, map[string]any{"columns": labels})
	}
	for _, op := range s.SetOps {
		right := pb.selectPlan(op.Right)
		props := map[string]any{"operation": strings.ToUpper(op.Op)}
		if op.All {
			props["all"] = true
		}
		node = pb.wrap(strings.Title(strings.ToLower(op.Op)), "", node, props)
		node.Children = append(node.Children, right)
	}
	if len(s.OrderBy) > 0 {
		var parts []string
		for _, o := range s.OrderBy {
			p := o.Expr.String()
			if o.Desc {
				p += " DESC"
			}
			parts = append(parts, p)
		}
		node = pb.wrap("Sort", strings.Join(parts, ", "), node, map[string]any{"orderBy": parts})
	}
	if s.Offset != nil && *s.Offset > 0 {
		props := map[string]any{"offset": *s.Offset}
		if s.Limit != nil {
			props["limit"] = *s.Limit
		}
		node = pb.wrap("Limit", "", node, props)
	} else if s.Limit != nil {
		node = pb.wrap("Limit", "*", node, map[string]any{"limit": *s.Limit})
	}
	for _, stage := range s.Pipes {
		node = pb.pipePlan(stage, node)
	}
	if len(s.CTEs) > 0 {
		names := make([]string, len(s.CTEs))
		for i, c := range s.CTEs {
			names[i] = c.Name
		}
		if node.Properties == nil {
			node.Properties = map[string]any{}
		}
		node.Properties["ctes"] = names
	}
	return node
}

func (pb *planBuilder) fromPlan(from []sql.TableExpr, hints map[*sql.TableRef]*scanHint) *PlanNode {
	if len(from) == 0 {
		return &PlanNode{ID: pb.id(), Operator: "Values", Detail: "single row"}
	}
	node := pb.tableExprPlan(from[0], hints)
	for _, te := range from[1:] {
		right := pb.tableExprPlan(te, hints)
		join := &PlanNode{ID: pb.id(), Operator: "Cross Join", Children: []*PlanNode{node, right}}
		node = join
	}
	return node
}

func (pb *planBuilder) tableExprPlan(te sql.TableExpr, hints map[*sql.TableRef]*scanHint) *PlanNode {
	switch t := te.(type) {
	case *sql.TableRef:
		if t.Source == "" {
			return &PlanNode{ID: pb.id(), Operator: "CTE Scan", Detail: t.Table}
		}
		props := map[string]any{"source": t.Source, "table": t.Table}
		h := hints[t]
		if c, err := pb.ec.connectorFor(t.Source); err == nil {
			caps := c.Capabilities()
			baseLimit := pb.ec.engine.Opts.MaxSourceRows + 1
			req := connector.FetchRequest{Table: t.Table, Limit: baseLimit}
			pushedLimit := false
			if h != nil {
				if caps.ProjectionPushdown && len(h.columns) > 0 {
					req.Columns = h.columns
				}
				if caps.FilterPushdown && len(h.predicates) > 0 {
					req.Predicates = h.predicates
				}
				if caps.LimitPushdown && h.limit > 0 && h.limit < req.Limit {
					req.Limit = h.limit
					pushedLimit = true
				}
			}
			if len(req.Columns) > 0 {
				props["pushedColumns"] = req.Columns
			}
			if len(req.Predicates) > 0 {
				var preds []string
				for _, p := range req.Predicates {
					preds = append(preds, p.Column+" "+p.Op+" ?")
				}
				props["pushedPredicates"] = preds
			}
			if pushedLimit {
				props["pushedLimit"] = req.Limit
			}
			if d, ok := c.(connector.StatementDescriber); ok {
				if statement, args, err := d.DescribeFetch(req); err == nil && statement != "" {
					props["sourceStatement"] = statement
					if len(args) > 0 {
						props["sourceArgs"] = args
					}
				}
			}
		}
		return &PlanNode{ID: pb.id(), Operator: "Source Scan", Detail: t.Source + "." + t.Table, Properties: props}
	case *sql.DerivedTable:
		child := pb.selectPlan(t.Query)
		return &PlanNode{ID: pb.id(), Operator: "Subquery", Detail: t.Alias, Children: []*PlanNode{child}}
	case *sql.JoinExpr:
		left := pb.tableExprPlan(t.Left, hints)
		right := pb.tableExprPlan(t.Right, hints)
		props := map[string]any{"joinType": strings.ToUpper(string(t.Type))}
		detail := strings.ToUpper(string(t.Type)) + " JOIN"
		if t.On != nil {
			props["on"] = t.On.String()
			detail += " ON " + t.On.String()
		}
		if len(t.Using) > 0 {
			props["using"] = t.Using
			detail += " USING (" + strings.Join(t.Using, ", ") + ")"
		}
		if t.Natural {
			props["natural"] = true
		}
		operator := "Nested Loop Join"
		if hasEquiJoin(t.On, t.Using) {
			operator = "Hash Join"
			props["algorithm"] = "hash"
		} else {
			props["algorithm"] = "nested-loop"
		}
		return &PlanNode{ID: pb.id(), Operator: operator, Detail: detail, Properties: props, Children: []*PlanNode{left, right}}
	default:
		return &PlanNode{ID: pb.id(), Operator: "Table", Detail: "unknown"}
	}
}
func (pb *planBuilder) wrap(operator, detail string, child *PlanNode, props map[string]any) *PlanNode {
	return &PlanNode{ID: pb.id(), Operator: operator, Detail: detail, Properties: props, Children: []*PlanNode{child}}
}

func (pb *planBuilder) pipePlan(stage sql.PipeStage, child *PlanNode) *PlanNode {
	switch p := stage.(type) {
	case *sql.PipeWhere:
		return pb.wrap("Filter", p.Expr.String(), child, map[string]any{"pipe": true, "predicate": p.Expr.String()})
	case *sql.PipeSelect:
		var labels []string
		for _, item := range p.Items {
			labels = append(labels, sql.Label(item))
		}
		return pb.wrap("Project", strings.Join(labels, ", "), child, map[string]any{"pipe": true, "columns": labels})
	case *sql.PipeAggregate:
		return pb.wrap("Aggregate", summarizeGroupingExprs(p.GroupBy), child, map[string]any{"pipe": true, "groupBy": exprStrings(p.GroupBy)})
	case *sql.PipeOrder:
		return pb.wrap("Sort", "", child, map[string]any{"pipe": true})
	case *sql.PipeLimit:
		return pb.wrap("Limit", "", child, map[string]any{"pipe": true, "limit": p.Limit, "offset": p.Offset})
	case *sql.PipeDistinct:
		return pb.wrap("Distinct", "", child, map[string]any{"pipe": true})
	case *sql.PipeJoin:
		right := pb.tableExprPlan(p.Right, pb.ec.scanHints)
		props := map[string]any{"joinType": strings.ToUpper(string(p.Type)), "pipe": true}
		return &PlanNode{ID: pb.id(), Operator: "Hash Join", Detail: "PIPE JOIN", Properties: props, Children: []*PlanNode{child, right}}
	default:
		return pb.wrap("Pipe", "", child, nil)
	}
}

func exprStrings(exprs []sql.Expr) []string {
	out := make([]string, len(exprs))
	for i, e := range exprs {
		out[i] = e.String()
	}
	return out
}

func summarizeGrouping(s *sql.SelectStmt) string {
	if len(s.GroupingSets) > 0 {
		return "GROUPING SETS"
	}
	if len(s.GroupBy) > 0 {
		return "GROUP BY " + strings.Join(exprStrings(s.GroupBy), ", ")
	}
	return "global aggregate"
}

func summarizeGroupingExprs(exprs []sql.Expr) string {
	if len(exprs) == 0 {
		return "global aggregate"
	}
	return "GROUP BY " + strings.Join(exprStrings(exprs), ", ")
}

func aggregateNames(s *sql.SelectStmt) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range s.Projections {
		if item.Expr == nil {
			continue
		}
		sql.WalkExpr(item.Expr, func(e sql.Expr) bool {
			if f, ok := e.(*sql.FuncCall); ok && f.Window == nil && sql.IsAggregate(f.Name) {
				name := strings.ToUpper(f.Name)
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
			return true
		})
	}
	return out
}

func windowNames(s *sql.SelectStmt) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range s.Projections {
		if item.Expr == nil {
			continue
		}
		sql.WalkExpr(item.Expr, func(e sql.Expr) bool {
			if f, ok := e.(*sql.FuncCall); ok && f.Window != nil {
				name := strings.ToUpper(f.Name)
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
			return true
		})
	}
	return out
}

func hasEquiJoin(on sql.Expr, using []string) bool {
	if len(using) > 0 {
		return true
	}
	found := false
	sql.WalkExpr(on, func(e sql.Expr) bool {
		if c, ok := e.(*sql.CompareExpr); ok && c.Op == "=" {
			found = true
			return false
		}
		return true
	})
	return found
}
