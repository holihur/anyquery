package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// Parser parses AnyQuery SQL. It is a hand-written recursive descent + Pratt
// parser so new statements/stages/operators are a local change.
type Parser struct {
	tokens []Token
	pos    int
	cur    Token
	peek   Token
	src    string
}

// Parse parses one statement.
func Parse(src string) (Statement, error) {
	tokens, err := tokenizeAll(src)
	if err != nil {
		return nil, err
	}
	p := &Parser{tokens: tokens, src: src}
	p.sync()
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	if p.cur.IsPunct(";") {
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if p.cur.Kind != TokEOF {
		return nil, p.errf("unexpected %s after statement", p.cur)
	}
	return stmt, nil
}

func (p *Parser) parseStatement() (Statement, error) {
	switch {
	case p.cur.IsKeyword("WITH"):
		return p.parseSelect()
	case p.cur.IsKeyword("SELECT"):
		return p.parseSelect()
	case p.cur.IsKeyword("FROM"):
		return p.parseFromFirst()
	case p.cur.IsKeyword("SHOW"):
		return p.parseShow()
	case p.cur.IsKeyword("DESCRIBE"), p.cur.IsKeyword("DESC"):
		return p.parseDescribe()
	case p.cur.IsKeyword("EXPLAIN"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		return &ExplainStmt{Stmt: stmt}, nil
	default:
		return nil, p.errf("expected SELECT, FROM, SHOW, DESCRIBE or EXPLAIN, got %s", p.cur)
	}
}

func (p *Parser) parseSelect() (*SelectStmt, error) {
	s, err := p.parseSelectBody()
	if err != nil {
		return nil, err
	}
	s, err = p.parseSetOperations(s, 1)
	if err != nil {
		return nil, err
	}
	if err := p.parseSelectTail(s); err != nil {
		return nil, err
	}
	return s, nil
}

// parseSetOperations parses UNION / INTERSECT / EXCEPT using precedence:
// INTERSECT binds tighter than UNION and EXCEPT.
func (p *Parser) parseSetOperations(left *SelectStmt, minPrec int) (*SelectStmt, error) {
	for {
		op, prec, ok := p.parseSetOperator()
		if !ok || prec < minPrec {
			return left, nil
		}
		all := false
		if p.cur.IsKeyword("ALL") {
			all = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if p.cur.IsKeyword("DISTINCT") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		right, err := p.parseSetTerm(prec + 1)
		if err != nil {
			return nil, err
		}
		left.SetOps = append(left.SetOps, SetOperation{Op: op, All: all, Right: right})
	}
}

func (p *Parser) parseSetTerm(minPrec int) (*SelectStmt, error) {
	if p.cur.IsPunct("(") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return right, nil
	}
	right, err := p.parseSelectBody()
	if err != nil {
		return nil, err
	}
	return p.parseSetOperations(right, minPrec)
}

func (p *Parser) parseSetOperator() (string, int, bool) {
	for _, op := range []struct {
		name string
		prec int
	}{{"UNION", 1}, {"EXCEPT", 1}, {"INTERSECT", 2}} {
		if p.cur.IsKeyword(op.name) {
			_ = p.advance()
			return op.name, op.prec, true
		}
	}
	return "", 0, false
}

func (p *Parser) parseSelectBody() (*SelectStmt, error) {
	s := &SelectStmt{}
	if p.cur.IsKeyword("WITH") {
		ctes, err := p.parseCTEs()
		if err != nil {
			return nil, err
		}
		s.CTEs = ctes
	}
	if !p.cur.IsKeyword("SELECT") {
		return nil, p.errf("expected SELECT, got %s", p.cur)
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.cur.IsKeyword("DISTINCT") {
		s.Distinct = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	} else if p.cur.IsKeyword("ALL") {
		// SELECT ALL is the explicit spelling of the default.
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	items, err := p.parseSelectItems()
	if err != nil {
		return nil, err
	}
	s.Projections = items
	if p.cur.IsKeyword("FROM") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		from, err := p.parseFromList()
		if err != nil {
			return nil, err
		}
		s.From = from
	}
	if err := p.parseSelectClauses(s); err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Parser) parseSelectClauses(s *SelectStmt) error {
	var err error
	if p.cur.IsKeyword("WHERE") {
		if err := p.advance(); err != nil {
			return err
		}
		if s.Where, err = p.parseExpr(0); err != nil {
			return err
		}
	}
	if p.cur.IsKeyword("GROUP") {
		if err := p.expectKeyword("GROUP"); err != nil {
			return err
		}
		if err := p.expectKeyword("BY"); err != nil {
			return err
		}
		if s.GroupBy, err = p.parseExprList(); err != nil {
			return err
		}
	}
	if p.cur.IsKeyword("HAVING") {
		if err := p.advance(); err != nil {
			return err
		}
		if s.Having, err = p.parseExpr(0); err != nil {
			return err
		}
	}
	return nil
}

// parseFromFirst supports FROM-first pipeline queries:
//
//	FROM mongo.orders |> WHERE status = 'paid' |> AGGREGATE count(*) AS n
func (p *Parser) parseFromFirst() (*SelectStmt, error) {
	if err := p.advance(); err != nil { // FROM
		return nil, err
	}
	from, err := p.parseFromList()
	if err != nil {
		return nil, err
	}
	s := &SelectStmt{From: from, Projections: []SelectItem{{Star: true}}}
	if err := p.parseSelectClauses(s); err != nil {
		return nil, err
	}
	if p.cur.IsPunct("|>") {
		if err := p.parsePipes(s); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := p.parseSelectTail(s); err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Parser) parseSelectTail(s *SelectStmt) error {
	var err error
	if p.cur.IsKeyword("ORDER") {
		if err := p.advance(); err != nil {
			return err
		}
		if err := p.expectKeyword("BY"); err != nil {
			return err
		}
		if s.OrderBy, err = p.parseOrderItems(); err != nil {
			return err
		}
	}
	if p.cur.IsKeyword("LIMIT") {
		limit, offset, err := p.parseLimit()
		if err != nil {
			return err
		}
		s.Limit, s.Offset = limit, offset
	}
	if p.cur.IsKeyword("OFFSET") {
		n, err := p.parseIntLiteral("OFFSET")
		if err != nil {
			return err
		}
		if p.cur.IsKeyword("ROW") || p.cur.IsKeyword("ROWS") {
			if err := p.advance(); err != nil {
				return err
			}
		}
		s.Offset = &n
	}
	if p.cur.IsKeyword("FETCH") {
		if err := p.advance(); err != nil {
			return err
		}
		if p.cur.IsKeyword("FIRST") || p.cur.IsKeyword("NEXT") {
			if err := p.advance(); err != nil {
				return err
			}
		}
		n, err := p.parseIntLiteral("FETCH")
		if err != nil {
			return err
		}
		if p.cur.IsKeyword("ROW") || p.cur.IsKeyword("ROWS") {
			if err := p.advance(); err != nil {
				return err
			}
		}
		if p.cur.IsKeyword("ONLY") {
			if err := p.advance(); err != nil {
				return err
			}
		}
		s.Limit = &n
	}
	return p.parsePipes(s)
}

func (p *Parser) parsePipes(s *SelectStmt) error {
	for p.cur.IsPunct("|>") {
		if err := p.advance(); err != nil {
			return err
		}
		stage, err := p.parsePipeStage()
		if err != nil {
			return err
		}
		s.Pipes = append(s.Pipes, stage)
	}
	return nil
}

func (p *Parser) parsePipeStage() (PipeStage, error) {
	switch {
	case p.cur.IsKeyword("WHERE"), p.cur.IsKeyword("FILTER"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		return &PipeWhere{Expr: e}, nil
	case p.cur.IsKeyword("SELECT"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		distinct := false
		if p.cur.IsKeyword("DISTINCT") {
			distinct = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		items, err := p.parseSelectItems()
		if err != nil {
			return nil, err
		}
		return &PipeSelect{Items: items, Distinct: distinct}, nil
	case p.cur.IsKeyword("AGGREGATE"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		items, err := p.parseSelectItems()
		if err != nil {
			return nil, err
		}
		var groups []Expr
		if p.cur.IsKeyword("GROUP") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			if err := p.expectKeyword("BY"); err != nil {
				return nil, err
			}
			if groups, err = p.parseExprList(); err != nil {
				return nil, err
			}
		} else if p.cur.IsKeyword("BY") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			if groups, err = p.parseExprList(); err != nil {
				return nil, err
			}
		}
		return &PipeAggregate{Items: items, GroupBy: groups}, nil
	case p.cur.IsKeyword("ORDER"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		items, err := p.parseOrderItems()
		if err != nil {
			return nil, err
		}
		return &PipeOrder{Items: items}, nil
	case p.cur.IsKeyword("LIMIT"):
		limit, offset, err := p.parseLimit()
		if err != nil {
			return nil, err
		}
		if limit == nil {
			return nil, p.errf("LIMIT value is required")
		}
		off := 0
		if offset != nil {
			off = *offset
		}
		return &PipeLimit{Limit: *limit, Offset: off}, nil
	case p.cur.IsKeyword("DISTINCT"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &PipeDistinct{}, nil
	case p.cur.IsKeyword("JOIN"), p.cur.IsKeyword("INNER"), p.cur.IsKeyword("LEFT"), p.cur.IsKeyword("RIGHT"), p.cur.IsKeyword("FULL"), p.cur.IsKeyword("CROSS"), p.cur.IsKeyword("NATURAL"):
		return p.parsePipeJoin()
	default:
		return nil, p.errf("unknown pipeline stage starting at %s", p.cur)
	}
}

func (p *Parser) parsePipeJoin() (PipeStage, error) {
	jt := JoinInner
	natural := false
	if p.cur.IsKeyword("NATURAL") {
		natural = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	switch {
	case p.cur.IsKeyword("INNER"):
		if err := p.advance(); err != nil {
			return nil, err
		}
	case p.cur.IsKeyword("LEFT"):
		jt = JoinLeft
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("OUTER") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
	case p.cur.IsKeyword("RIGHT"):
		jt = JoinRight
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("OUTER") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
	case p.cur.IsKeyword("FULL"):
		jt = JoinFull
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("OUTER") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
	case p.cur.IsKeyword("CROSS"):
		jt = JoinCross
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if err := p.expectKeyword("JOIN"); err != nil {
		return nil, err
	}
	right, err := p.parseTablePrimary()
	if err != nil {
		return nil, err
	}
	stage := &PipeJoin{Right: right, Type: jt, Natural: natural}
	if jt != JoinCross {
		if p.cur.IsKeyword("ON") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			if stage.On, err = p.parseExpr(0); err != nil {
				return nil, err
			}
		} else if p.cur.IsKeyword("USING") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			if stage.Using, err = p.parseIdentList(); err != nil {
				return nil, err
			}
		}
	}
	return stage, nil
}

func (p *Parser) parseCTEs() ([]*CTE, error) {
	if err := p.expectKeyword("WITH"); err != nil {
		return nil, err
	}
	recursive := false
	if p.cur.IsKeyword("RECURSIVE") {
		recursive = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	var out []*CTE
	for {
		name, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		c := &CTE{Name: name, Recursive: recursive}
		if p.cur.IsPunct("(") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			for {
				col, err := p.parseIdent()
				if err != nil {
					return nil, err
				}
				c.Columns = append(c.Columns, col)
				if !p.cur.IsPunct(",") {
					break
				}
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
		}
		if err := p.expectKeyword("AS"); err != nil {
			return nil, err
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		c.Query = sub
		out = append(out, c)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *Parser) parseSelectItems() ([]SelectItem, error) {
	var out []SelectItem
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, p.errf("expected at least one select expression")
	}
	return out, nil
}

func (p *Parser) parseSelectItem() (SelectItem, error) {
	if p.cur.IsPunct("*") {
		if err := p.advance(); err != nil {
			return SelectItem{}, err
		}
		return SelectItem{Star: true}, nil
	}
	// Detect table.* / source.table.* with lookahead, then rewind. This keeps
	// the normal expression path available for u.age + 1 and friends.
	if p.cur.Kind == TokIdent {
		save := p.pos
		parts := []string{p.cur.Value}
		if err := p.advance(); err != nil {
			return SelectItem{}, err
		}
		for p.cur.IsPunct(".") {
			if p.peek.IsPunct("*") {
				if err := p.advance(); err != nil { // dot
					return SelectItem{}, err
				}
				if err := p.advance(); err != nil { // star
					return SelectItem{}, err
				}
				return SelectItem{Star: true, StarTable: strings.Join(parts, ".")}, nil
			}
			if p.peek.Kind != TokIdent {
				break
			}
			if err := p.advance(); err != nil { // dot
				return SelectItem{}, err
			}
			parts = append(parts, p.cur.Value)
			if err := p.advance(); err != nil {
				return SelectItem{}, err
			}
		}
		p.pos = save
		p.sync()
	}
	expr, err := p.parseExpr(0)
	if err != nil {
		return SelectItem{}, err
	}
	return p.finishSelectItem(expr)
}

func (p *Parser) finishSelectItem(expr Expr) (SelectItem, error) {
	item := SelectItem{Expr: expr}
	if p.cur.IsKeyword("AS") {
		if err := p.advance(); err != nil {
			return SelectItem{}, err
		}
		alias, err := p.parseIdent()
		if err != nil {
			return SelectItem{}, err
		}
		item.Alias = alias
	} else if p.cur.Kind == TokIdent && !isClauseKeyword(p.cur.Value) {
		item.Alias = p.cur.Value
		if err := p.advance(); err != nil {
			return SelectItem{}, err
		}
	}
	return item, nil
}

func (p *Parser) parseFromList() ([]TableExpr, error) {
	var out []TableExpr
	for {
		t, err := p.parseTableExpr()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *Parser) parseTableExpr() (TableExpr, error) {
	left, err := p.parseTablePrimary()
	if err != nil {
		return nil, err
	}
	for {
		jt := JoinType("")
		natural := false
		if p.cur.IsKeyword("NATURAL") && (p.peek.IsKeyword("JOIN") || p.peek.IsKeyword("INNER") || p.peek.IsKeyword("LEFT") || p.peek.IsKeyword("RIGHT") || p.peek.IsKeyword("FULL") || p.peek.IsKeyword("CROSS")) {
			natural = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		switch {
		case p.cur.IsKeyword("JOIN"), p.cur.IsKeyword("INNER"):
			jt = JoinInner
			if p.cur.IsKeyword("INNER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
			if err := p.expectKeyword("JOIN"); err != nil {
				return nil, err
			}
		case p.cur.IsKeyword("LEFT"):
			jt = JoinLeft
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.IsKeyword("OUTER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
			if err := p.expectKeyword("JOIN"); err != nil {
				return nil, err
			}
		case p.cur.IsKeyword("RIGHT"):
			jt = JoinRight
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.IsKeyword("OUTER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
			if err := p.expectKeyword("JOIN"); err != nil {
				return nil, err
			}
		case p.cur.IsKeyword("FULL"):
			jt = JoinFull
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.cur.IsKeyword("OUTER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
			if err := p.expectKeyword("JOIN"); err != nil {
				return nil, err
			}
		case p.cur.IsKeyword("CROSS"):
			jt = JoinCross
			if err := p.advance(); err != nil {
				return nil, err
			}
			if err := p.expectKeyword("JOIN"); err != nil {
				return nil, err
			}
		default:
			return left, nil
		}
		right, err := p.parseTablePrimary()
		if err != nil {
			return nil, err
		}
		j := &JoinExpr{Left: left, Right: right, Type: jt, Natural: natural}
		if jt != JoinCross {
			if p.cur.IsKeyword("ON") {
				if err := p.advance(); err != nil {
					return nil, err
				}
				if j.On, err = p.parseExpr(0); err != nil {
					return nil, err
				}
			} else if p.cur.IsKeyword("USING") {
				if err := p.advance(); err != nil {
					return nil, err
				}
				if j.Using, err = p.parseIdentList(); err != nil {
					return nil, err
				}
			}
		}
		left = j
	}
}

func (p *Parser) parseTablePrimary() (TableExpr, error) {
	if p.cur.IsPunct("(") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("VALUES") {
			return p.parseValuesTable()
		}
		if !p.cur.IsKeyword("SELECT") && !p.cur.IsKeyword("WITH") && !p.cur.IsKeyword("FROM") {
			return nil, p.errf("expected subquery or VALUES after '(' in FROM")
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		if err := p.expectKeyword("AS"); err != nil {
			// alias without AS
			if p.cur.Kind != TokIdent || isClauseKeyword(p.cur.Value) {
				return nil, p.errf("subquery in FROM must have an alias")
			}
		}
		alias, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		return &DerivedTable{Query: sub, Alias: alias}, nil
	}

	parts, err := p.parseQualifiedName()
	if err != nil {
		return nil, err
	}
	ref := &TableRef{}
	if len(parts) == 1 {
		// Could be a CTE or a single-table source; the binder validates it.
		ref.Table = parts[0]
	} else {
		ref.Source = parts[0]
		ref.Table = strings.Join(parts[1:], ".")
	}
	if p.cur.IsKeyword("AS") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		ref.Alias, err = p.parseIdent()
		if err != nil {
			return nil, err
		}
	} else if p.cur.Kind == TokIdent && !isClauseKeyword(p.cur.Value) {
		ref.Alias = p.cur.Value
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return ref, nil
}

func (p *Parser) parseValuesTable() (TableExpr, error) {
	if err := p.advance(); err != nil { // VALUES
		return nil, err
	}
	vt := &ValuesTable{}
	for {
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		var row []Expr
		for {
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			row = append(row, e)
			if !p.cur.IsPunct(",") {
				break
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		vt.Rows = append(vt.Rows, row)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("AS"); err != nil {
		if p.cur.Kind != TokIdent || isClauseKeyword(p.cur.Value) {
			return nil, p.errf("VALUES table must have an alias")
		}
	}
	alias, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	vt.Alias = alias
	if p.cur.IsPunct("(") {
		cols, err := p.parseIdentList()
		if err != nil {
			return nil, err
		}
		vt.Columns = cols
	}
	return vt, nil
}

func (p *Parser) parseQualifiedName() ([]string, error) {
	var parts []string
	for {
		id, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		parts = append(parts, id)
		if !p.cur.IsPunct(".") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

func (p *Parser) parseIdentList() ([]string, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	var out []string
	for {
		id, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		out = append(out, id)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Parser) parseExprList() ([]Expr, error) {
	var out []Expr
	for {
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *Parser) parseOrderItems() ([]OrderItem, error) {
	var out []OrderItem
	for {
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		item := OrderItem{Expr: e}
		if p.cur.IsKeyword("ASC") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if p.cur.IsKeyword("DESC") {
			item.Desc = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		if p.cur.IsKeyword("NULLS") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			switch {
			case p.cur.IsKeyword("FIRST"):
				item.Nulls = "first"
			case p.cur.IsKeyword("LAST"):
				item.Nulls = "last"
			default:
				return nil, p.errf("expected FIRST or LAST after NULLS")
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		out = append(out, item)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *Parser) parseLimit() (*int, *int, error) {
	if err := p.expectKeyword("LIMIT"); err != nil {
		return nil, nil, err
	}
	if p.cur.IsKeyword("ALL") {
		if err := p.advance(); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	first, err := p.parseIntLiteral("LIMIT")
	if err != nil {
		return nil, nil, err
	}
	if p.cur.IsPunct(",") {
		if err := p.advance(); err != nil {
			return nil, nil, err
		}
		second, err := p.parseIntLiteral("LIMIT")
		if err != nil {
			return nil, nil, err
		}
		// MySQL form LIMIT offset, count
		return &second, &first, nil
	}
	if p.cur.IsKeyword("OFFSET") {
		if err := p.advance(); err != nil {
			return nil, nil, err
		}
		off, err := p.parseIntLiteral("OFFSET")
		if err != nil {
			return nil, nil, err
		}
		return &first, &off, nil
	}
	return &first, nil, nil
}

func (p *Parser) parseIntLiteral(what string) (int, error) {
	if p.cur.Kind != TokNumber {
		return 0, p.errf("%s expects a number, got %s", what, p.cur)
	}
	f, err := strconv.ParseFloat(p.cur.Value, 64)
	if err != nil || f < 0 || f != float64(int(f)) {
		return 0, p.errf("%s expects a non-negative integer", what)
	}
	n := int(f)
	if err := p.advance(); err != nil {
		return 0, err
	}
	return n, nil
}

// ---- expressions -----------------------------------------------------------

// Precedence levels.
const (
	precOr      = 1
	precAnd     = 2
	precNot     = 3
	precCompare = 4
	precBitOr   = 5
	precBitAnd  = 6
	precConcat  = 7
	precAdd     = 8
	precMul     = 9
)

func (p *Parser) parseExpr(minPrec int) (Expr, error) {
	var left Expr
	var err error

	// Prefix operators.
	switch {
	case p.cur.IsKeyword("NOT"):
		if precNot < minPrec {
			return nil, p.errf("unexpected NOT")
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseExpr(precNot)
		if err != nil {
			return nil, err
		}
		left = &UnaryExpr{Op: "NOT", Expr: operand}
	case p.cur.IsPunct("-"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseExpr(precMul)
		if err != nil {
			return nil, err
		}
		left = &UnaryExpr{Op: "-", Expr: operand}
	case p.cur.IsPunct("+"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseExpr(precMul)
		if err != nil {
			return nil, err
		}
		left = &UnaryExpr{Op: "+", Expr: operand}
	case p.cur.IsPunct("~"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseExpr(precMul)
		if err != nil {
			return nil, err
		}
		left = &UnaryExpr{Op: "~", Expr: operand}
	default:
		left, err = p.parsePrimary()
		if err != nil {
			return nil, err
		}
	}

	// Postfix CAST.
	if p.cur.IsPunct("::") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		typ, err := p.parseTypeName()
		if err != nil {
			return nil, err
		}
		left = &CastExpr{Expr: left, Type: typ}
	}

	for {
		// Arithmetic / boolean infix.
		if prec, op, ok := binaryOp(p.cur); ok && prec >= minPrec {
			if err := p.advance(); err != nil {
				return nil, err
			}
			right, err := p.parseExpr(prec + 1)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: op, Left: left, Right: right}
			continue
		}

		// Comparison-level handling.
		if precCompare < minPrec {
			return left, nil
		}
		if cmp, err := p.parseComparison(left); err != nil {
			return nil, err
		} else if cmp != nil {
			left = cmp
			continue
		}
		return left, nil
	}
}

func (p *Parser) parseComparison(left Expr) (Expr, error) {
	negated := false
	if p.cur.IsKeyword("NOT") {
		// Only consume NOT when followed by IN / LIKE / ILIKE / BETWEEN.
		if !(p.peek.IsKeyword("IN") || p.peek.IsKeyword("LIKE") || p.peek.IsKeyword("ILIKE") || p.peek.IsKeyword("BETWEEN")) {
			return nil, nil
		}
		negated = true
		if err := p.advance(); err != nil {
			return nil, err
		}
	}

	switch {
	case p.cur.IsPunct("=") || p.cur.IsPunct("==") || p.cur.IsPunct("!=") || p.cur.IsPunct("<>") ||
		p.cur.IsPunct("<") || p.cur.IsPunct("<=") || p.cur.IsPunct(">") || p.cur.IsPunct(">="):
		op := p.cur.Value
		if op == "==" {
			op = "="
		}
		if op == "<>" {
			op = "!="
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("ANY") || p.cur.IsKeyword("SOME") || p.cur.IsKeyword("ALL") {
			all := p.cur.IsKeyword("ALL")
			if err := p.advance(); err != nil {
				return nil, err
			}
			if err := p.expectPunct("("); err != nil {
				return nil, err
			}
			q := &QuantifiedExpr{Op: op, Left: left, All: all}
			if p.cur.IsKeyword("SELECT") || p.cur.IsKeyword("WITH") {
				sub, err := p.parseSelect()
				if err != nil {
					return nil, err
				}
				q.Subquery = sub
			} else {
				for {
					e, err := p.parseExpr(0)
					if err != nil {
						return nil, err
					}
					q.List = append(q.List, e)
					if !p.cur.IsPunct(",") {
						break
					}
					if err := p.advance(); err != nil {
						return nil, err
					}
				}
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return q, nil
		}
		right, err := p.parseExpr(precCompare + 1)
		if err != nil {
			return nil, err
		}
		return &CompareExpr{Op: op, Left: left, Right: right, Negated: negated}, nil

	case p.cur.IsKeyword("LIKE"), p.cur.IsKeyword("ILIKE"), p.cur.IsKeyword("REGEXP"):
		op := strings.ToUpper(p.cur.Value)
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseExpr(precCompare + 1)
		if err != nil {
			return nil, err
		}
		return &CompareExpr{Op: op, Left: left, Right: right, Negated: negated}, nil

	case p.cur.IsKeyword("IN"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		in := &InExpr{Left: left, Negated: negated}
		if p.cur.IsKeyword("SELECT") || p.cur.IsKeyword("WITH") {
			sub, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			in.Subquery = sub
		} else {
			for {
				e, err := p.parseExpr(0)
				if err != nil {
					return nil, err
				}
				in.List = append(in.List, e)
				if !p.cur.IsPunct(",") {
					break
				}
				if err := p.advance(); err != nil {
					return nil, err
				}
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return in, nil

	case p.cur.IsKeyword("BETWEEN"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		low, err := p.parseExpr(precCompare + 1)
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("AND"); err != nil {
			return nil, err
		}
		high, err := p.parseExpr(precCompare + 1)
		if err != nil {
			return nil, err
		}
		return &BetweenExpr{Left: left, Low: low, High: high, Negated: negated}, nil

	case p.cur.IsKeyword("IS"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		isNeg := false
		if p.cur.IsKeyword("NOT") {
			isNeg = true
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		if p.cur.IsKeyword("DISTINCT") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			if err := p.expectKeyword("FROM"); err != nil {
				return nil, err
			}
			right, err := p.parseExpr(precCompare + 1)
			if err != nil {
				return nil, err
			}
			return &IsExpr{Left: left, Right: right, What: "distinct", Negated: isNeg}, nil
		}
		var what string
		switch {
		case p.cur.IsKeyword("NULL"):
			what = "null"
		case p.cur.IsKeyword("TRUE"):
			what = "true"
		case p.cur.IsKeyword("FALSE"):
			what = "false"
		case p.cur.IsKeyword("UNKNOWN"):
			what = "null"
		default:
			return nil, p.errf("expected NULL, TRUE, FALSE or UNKNOWN after IS")
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &IsExpr{Left: left, What: what, Negated: isNeg}, nil
	}

	if negated {
		return nil, p.errf("expected IN, LIKE, ILIKE or BETWEEN after NOT")
	}
	return nil, nil
}

func binaryOp(t Token) (int, string, bool) {
	if t.Kind == TokPunct {
		switch t.Value {
		case "|":
			return precBitOr, "|", true
		case "&":
			return precBitAnd, "&", true
		case "||":
			return precConcat, "||", true
		case "+":
			return precAdd, "+", true
		case "-":
			return precAdd, "-", true
		case "*":
			return precMul, "*", true
		case "/":
			return precMul, "/", true
		case "%":
			return precMul, "%", true
		}
	}
	if t.Kind == TokIdent && !t.Quoted {
		switch strings.ToUpper(t.Value) {
		case "OR":
			return precOr, "OR", true
		case "AND":
			return precAnd, "AND", true
		}
	}
	return 0, "", false
}

func (p *Parser) parsePrimary() (Expr, error) {
	t := p.cur
	switch {
	case t.Kind == TokNumber:
		if strings.ContainsAny(t.Value, ".eE") {
			f, err := strconv.ParseFloat(t.Value, 64)
			if err != nil {
				return nil, p.errf("invalid number %q", t.Value)
			}
			if err := p.advance(); err != nil {
				return nil, err
			}
			return &Literal{Kind: LitFloat, Value: f}, nil
		}
		n, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return nil, p.errf("invalid integer %q", t.Value)
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Literal{Kind: LitInt, Value: n}, nil

	case t.Kind == TokString:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Literal{Kind: LitString, Value: t.Value}, nil

	case t.IsKeyword("TRUE"), t.IsKeyword("FALSE"):
		val := strings.EqualFold(t.Value, "TRUE")
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Literal{Kind: LitBool, Value: val}, nil

	case t.IsKeyword("NULL"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Literal{Kind: LitNull}, nil

	case t.IsKeyword("EXTRACT"):
		return p.parseExtract()
	case t.IsKeyword("POSITION"):
		return p.parsePosition()
	case t.IsKeyword("SUBSTRING"), t.IsKeyword("SUBSTR"):
		if p.peek.IsPunct("(") {
			return p.parseSubstring()
		}
		return nil, p.errf("expected '(' after %s", strings.ToUpper(t.Value))
	case t.IsKeyword("CASE"):
		return p.parseCase()

	case t.IsKeyword("CAST"):
		return p.parseCast()

	case t.IsKeyword("EXISTS"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return &ExistsExpr{Query: sub}, nil

	case t.IsKeyword("CURRENT_TIMESTAMP"), t.IsKeyword("CURRENT_DATE"), t.IsKeyword("CURRENT_TIME"), t.IsKeyword("NOW"):
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsPunct("(") {
			args, err := p.parseCallArgs()
			if err != nil {
				return nil, err
			}
			return &FuncCall{Name: strings.ToLower(t.Value), Args: args}, nil
		}
		return &FuncCall{Name: strings.ToLower(t.Value)}, nil

	case t.IsPunct("("):
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("SELECT") || p.cur.IsKeyword("WITH") {
			sub, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return &SubqueryExpr{Query: sub}, nil
		}
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return e, nil

	case t.Kind == TokIdent:
		// Function call?
		if p.peek.IsPunct("(") {
			name := t.Value
			if err := p.advance(); err != nil { // name
				return nil, err
			}
			args, distinct, err := p.parseCallArgsDistinct()
			if err != nil {
				return nil, err
			}
			fn := &FuncCall{Name: name, Args: args, Distinct: distinct}
			if p.cur.IsKeyword("FILTER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
				if err := p.expectPunct("("); err != nil {
					return nil, err
				}
				if err := p.expectKeyword("WHERE"); err != nil {
					return nil, err
				}
				if fn.Filter, err = p.parseExpr(0); err != nil {
					return nil, err
				}
				if err := p.expectPunct(")"); err != nil {
					return nil, err
				}
			}
			if p.cur.IsKeyword("OVER") {
				if err := p.advance(); err != nil {
					return nil, err
				}
				if fn.Window, err = p.parseWindowSpec(); err != nil {
					return nil, err
				}
			}
			return fn, nil
		}
		parts, err := p.parseQualifiedName()
		if err != nil {
			return nil, err
		}
		return &ColumnRef{Parts: parts}, nil
	}

	return nil, p.errf("expected expression, got %s", t)
}

func (p *Parser) parseWindowSpec() (*WindowSpec, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	w := &WindowSpec{}
	if p.cur.IsKeyword("PARTITION") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		var err error
		if w.PartitionBy, err = p.parseExprList(); err != nil {
			return nil, err
		}
	}
	if p.cur.IsKeyword("ORDER") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		var err error
		if w.OrderBy, err = p.parseOrderItems(); err != nil {
			return nil, err
		}
	}
	if p.cur.IsKeyword("ROWS") || p.cur.IsKeyword("RANGE") {
		w.FrameKind = strings.ToUpper(p.cur.Value)
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("BETWEEN") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			start, off, err := p.parseFrameBound()
			if err != nil {
				return nil, err
			}
			w.FrameStart, w.FrameStartOffset = start, off
			if err := p.expectKeyword("AND"); err != nil {
				return nil, err
			}
			end, off, err := p.parseFrameBound()
			if err != nil {
				return nil, err
			}
			w.FrameEnd, w.FrameEndOffset = end, off
		} else {
			start, off, err := p.parseFrameBound()
			if err != nil {
				return nil, err
			}
			w.FrameStart, w.FrameStartOffset = start, off
			w.FrameEnd = "current_row"
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return w, nil
}

func (p *Parser) parseFrameBound() (string, int, error) {
	if p.cur.IsKeyword("UNBOUNDED") {
		if err := p.advance(); err != nil {
			return "", 0, err
		}
		switch {
		case p.cur.IsKeyword("PRECEDING"):
			if err := p.advance(); err != nil {
				return "", 0, err
			}
			return "unbounded_preceding", 0, nil
		case p.cur.IsKeyword("FOLLOWING"):
			if err := p.advance(); err != nil {
				return "", 0, err
			}
			return "unbounded_following", 0, nil
		}
		return "", 0, p.errf("expected PRECEDING or FOLLOWING after UNBOUNDED")
	}
	if p.cur.IsKeyword("CURRENT") {
		if err := p.advance(); err != nil {
			return "", 0, err
		}
		if err := p.expectKeyword("ROW"); err != nil {
			return "", 0, err
		}
		return "current_row", 0, nil
	}
	if p.cur.Kind != TokNumber {
		return "", 0, p.errf("expected a frame bound")
	}
	n, err := p.parseIntLiteral("frame bound")
	if err != nil {
		return "", 0, err
	}
	switch {
	case p.cur.IsKeyword("PRECEDING"):
		if err := p.advance(); err != nil {
			return "", 0, err
		}
		return "preceding", n, nil
	case p.cur.IsKeyword("FOLLOWING"):
		if err := p.advance(); err != nil {
			return "", 0, err
		}
		return "following", n, nil
	}
	return "", 0, p.errf("expected PRECEDING or FOLLOWING after frame offset")
}

func (p *Parser) parseCallArgs() ([]Expr, error) {
	args, _, err := p.parseCallArgsDistinct()
	return args, err
}

func (p *Parser) parseCallArgsDistinct() ([]Expr, bool, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, false, err
	}
	if p.cur.IsPunct(")") {
		if err := p.advance(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	distinct := false
	if p.cur.IsKeyword("DISTINCT") {
		distinct = true
		if err := p.advance(); err != nil {
			return nil, false, err
		}
	}
	// COUNT(*)
	if p.cur.IsPunct("*") {
		if err := p.advance(); err != nil {
			return nil, false, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, false, err
		}
		return []Expr{&StarExpr{}}, distinct, nil
	}
	var args []Expr
	for {
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, false, err
		}
		args = append(args, e)
		if !p.cur.IsPunct(",") {
			break
		}
		if err := p.advance(); err != nil {
			return nil, false, err
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, false, err
	}
	return args, distinct, nil
}

func (p *Parser) parseExtract() (Expr, error) {
	if err := p.advance(); err != nil { // EXTRACT
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	part, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	e, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return &FuncCall{Name: "extract", Args: []Expr{&Literal{Kind: LitString, Value: part}, e}}, nil
}

func (p *Parser) parsePosition() (Expr, error) {
	if err := p.advance(); err != nil { // POSITION
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	sub, err := p.parseExpr(precCompare + 1)
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("IN"); err != nil {
		return nil, err
	}
	str, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return &FuncCall{Name: "position", Args: []Expr{sub, str}}, nil
}

func (p *Parser) parseSubstring() (Expr, error) {
	if err := p.advance(); err != nil { // SUBSTRING
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	str, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	args := []Expr{str}
	if p.cur.IsKeyword("FROM") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		start, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, start)
		if p.cur.IsKeyword("FOR") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			length, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			args = append(args, length)
		}
	} else if p.cur.IsPunct(",") {
		for p.cur.IsPunct(",") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			a, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			args = append(args, a)
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return &FuncCall{Name: "substring", Args: args}, nil
}

func (p *Parser) parseCase() (Expr, error) {
	if err := p.advance(); err != nil { // CASE
		return nil, err
	}
	c := &CaseExpr{}
	if !p.cur.IsKeyword("WHEN") {
		base, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		c.Base = base
	}
	for p.cur.IsKeyword("WHEN") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		cond, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("THEN"); err != nil {
			return nil, err
		}
		then, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		c.Whens = append(c.Whens, WhenClause{Cond: cond, Then: then})
	}
	if p.cur.IsKeyword("ELSE") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		c.Else = e
	}
	if err := p.expectKeyword("END"); err != nil {
		return nil, err
	}
	return c, nil
}

func (p *Parser) parseCast() (Expr, error) {
	if err := p.advance(); err != nil { // CAST
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	e, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("AS"); err != nil {
		return nil, err
	}
	typ, err := p.parseTypeName()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return &CastExpr{Expr: e, Type: typ}, nil
}

func (p *Parser) parseTypeName() (string, error) {
	id, err := p.parseIdent()
	if err != nil {
		return "", err
	}
	parts := []string{id}
	for p.cur.IsKeyword("WITHOUT") || p.cur.IsKeyword("WITH") {
		parts = append(parts, strings.ToUpper(p.cur.Value))
		if err := p.advance(); err != nil {
			return "", err
		}
		if p.cur.IsKeyword("TIME") {
			parts = append(parts, "TIME")
			if err := p.advance(); err != nil {
				return "", err
			}
			if p.cur.IsKeyword("ZONE") {
				parts = append(parts, "ZONE")
				if err := p.advance(); err != nil {
					return "", err
				}
			}
		}
	}
	if p.cur.IsPunct("(") {
		if err := p.advance(); err != nil {
			return "", err
		}
		var args []string
		for !p.cur.IsPunct(")") {
			if p.cur.Kind != TokNumber {
				return "", p.errf("expected type parameter")
			}
			args = append(args, p.cur.Value)
			if err := p.advance(); err != nil {
				return "", err
			}
			if p.cur.IsPunct(",") {
				if err := p.advance(); err != nil {
					return "", err
				}
			} else {
				break
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return "", err
		}
		return strings.Join(parts, " ") + "(" + strings.Join(args, ",") + ")", nil
	}
	return strings.Join(parts, " "), nil
}

func (p *Parser) parseShow() (Statement, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	st := &ShowStmt{}
	switch {
	case p.cur.IsKeyword("TABLES"):
		st.Target = "tables"
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("FROM") || p.cur.IsKeyword("IN") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			id, err := p.parseIdent()
			if err != nil {
				return nil, err
			}
			st.Source = id
		}
	case p.cur.IsKeyword("COLUMNS"):
		st.Target = "columns"
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.cur.IsKeyword("FROM") || p.cur.IsKeyword("IN") {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		parts, err := p.parseQualifiedName()
		if err != nil {
			return nil, err
		}
		if len(parts) < 2 {
			return nil, p.errf("SHOW COLUMNS needs source.table")
		}
		st.Source = parts[0]
		st.Table = strings.Join(parts[1:], ".")
	case p.cur.IsKeyword("SOURCES"), p.cur.IsKeyword("DATABASES"):
		st.Target = "sources"
		if err := p.advance(); err != nil {
			return nil, err
		}
	default:
		return nil, p.errf("expected TABLES, COLUMNS or SOURCES after SHOW")
	}
	return st, nil
}

func (p *Parser) parseDescribe() (Statement, error) {
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.cur.IsKeyword("TABLE") {
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	parts, err := p.parseQualifiedName()
	if err != nil {
		return nil, err
	}
	if len(parts) < 2 {
		return nil, p.errf("DESCRIBE needs source.table")
	}
	return &DescribeStmt{Source: parts[0], Table: strings.Join(parts[1:], ".")}, nil
}

// ---- token helpers ---------------------------------------------------------

func (p *Parser) sync() {
	p.cur = p.tokens[p.pos]
	if p.pos+1 < len(p.tokens) {
		p.peek = p.tokens[p.pos+1]
	} else {
		p.peek = Token{Kind: TokEOF}
	}
}

func (p *Parser) advance() error {
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	p.sync()
	return nil
}

func tokenizeAll(src string) ([]Token, error) {
	l := NewLexer(src)
	var out []Token
	for {
		t, err := l.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if t.Kind == TokEOF {
			return out, nil
		}
	}
}

func (p *Parser) expectKeyword(kw string) error {
	if !p.cur.IsKeyword(kw) {
		return p.errf("expected %s, got %s", strings.ToUpper(kw), p.cur)
	}
	return p.advance()
}

func (p *Parser) expectPunct(s string) error {
	if !p.cur.IsPunct(s) {
		return p.errf("expected %q, got %s", s, p.cur)
	}
	return p.advance()
}

func (p *Parser) parseIdent() (string, error) {
	if p.cur.Kind != TokIdent {
		return "", p.errf("expected identifier, got %s", p.cur)
	}
	v := p.cur.Value
	if err := p.advance(); err != nil {
		return "", err
	}
	return v, nil
}

func (p *Parser) errf(format string, args ...any) error {
	return fmt.Errorf("%s at line %d, column %d", fmt.Sprintf(format, args...), p.cur.Line, p.cur.Col)
}

func isClauseKeyword(s string) bool {
	switch strings.ToUpper(s) {
	case "FROM", "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT",
		"JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "NATURAL", "ON", "USING",
		"AND", "OR", "NOT", "IS", "IN", "LIKE", "ILIKE", "BETWEEN", "WHEN", "THEN", "ELSE", "END",
		"ASC", "DESC", "NULLS", "FIRST", "LAST", "AS", "BY", "SELECT", "DISTINCT", "ALL", "AGGREGATE", "FILTER", "OVER", "PARTITION", "ROWS", "RANGE", "UNBOUNDED", "PRECEDING", "FOLLOWING", "CURRENT", "ROW", "FETCH", "ONLY", "RECURSIVE", "ANY", "SOME", "PIPE":
		return true
	}
	return false
}
