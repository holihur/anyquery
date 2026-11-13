package graphql

import (
	"strings"

	"github.com/anyquery/anyquery/internal/connector"
)

// gqlField is the subset of a GraphQL selection set needed to describe result
// columns. Fragment spreads are intentionally not resolved; unsupported
// selections simply fall back to row inference at fetch time.
type gqlField struct {
	ResponseName string
	Name         string
	Spread       string
	Children     []*gqlField
}

type gqlToken struct {
	text string
}

func lexGraphQL(q string) []gqlToken {
	var out []gqlToken
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '#':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case c == '"':
			if strings.HasPrefix(q[i:], `"""`) {
				i += 3
				for i < len(q) && !strings.HasPrefix(q[i:], `"""`) {
					i++
				}
				if i < len(q) {
					i += 3
				}
				out = append(out, gqlToken{text: `"`})
				continue
			}
			start := i
			i++
			for i < len(q) {
				if q[i] == '\\' {
					i += 2
					continue
				}
				if q[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(q) {
				i = len(q)
			}
			out = append(out, gqlToken{text: q[start:i]})
		case isNameStart(c):
			start := i
			i++
			for i < len(q) && isNamePart(q[i]) {
				i++
			}
			out = append(out, gqlToken{text: q[start:i]})
		case c == '.' && strings.HasPrefix(q[i:], "..."):
			i += 3
			out = append(out, gqlToken{text: "..."})
		default:
			out = append(out, gqlToken{text: string(c)})
			i++
		}
	}
	return out
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func isNamePart(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}

type gqlParser struct {
	tokens    []gqlToken
	pos       int
	fragments map[string][]*gqlField
}

func (p *gqlParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos].text
}

func (p *gqlParser) skipBalanced(open, close string) {
	if p.peek() != open {
		return
	}
	depth := 0
	for p.pos < len(p.tokens) {
		switch p.tokens[p.pos].text {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				p.pos++
				return
			}
		}
		p.pos++
	}
}

func (p *gqlParser) scanFragments() {
	for p.pos < len(p.tokens) {
		if !strings.EqualFold(p.peek(), "fragment") {
			p.pos++
			continue
		}
		p.pos++
		name := p.peek()
		p.pos++
		if !strings.EqualFold(p.peek(), "on") {
			continue
		}
		p.pos++
		if p.pos < len(p.tokens) && p.peek() != "{" {
			p.pos++ // type condition
		}
		if p.peek() == "{" {
			p.fragments[name] = p.parseSelection()
		}
	}
}

func (p *gqlParser) parseDocument() []*gqlField {
	if p.fragments == nil {
		p.fragments = map[string][]*gqlField{}
	}
	p.scanFragments()
	p.pos = 0
	for p.pos < len(p.tokens) {
		switch strings.ToLower(p.peek()) {
		case "query", "mutation", "subscription":
			p.pos++
			// Optional operation name and variable definitions.
			if p.peek() != "{" && p.peek() != "@" && p.peek() != "(" {
				p.pos++
			}
			for p.peek() == "(" {
				p.skipBalanced("(", ")")
			}
			for p.peek() == "@" {
				p.pos++
				if p.pos < len(p.tokens) {
					p.pos++
				}
				for p.peek() == "(" {
					p.skipBalanced("(", ")")
				}
			}
		case "{":
			return expandFields(p.parseSelection(), p.fragments, map[string]bool{})
		case "fragment":
			// Skip a fragment definition; query operations are normally first.
			for p.pos < len(p.tokens) && p.peek() != "{" {
				p.pos++
			}
			if p.peek() == "{" {
				_ = p.parseSelection()
			}
		default:
			p.pos++
		}
	}
	return nil
}

func (p *gqlParser) parseSelection() []*gqlField {
	if p.peek() != "{" {
		return nil
	}
	p.pos++
	var out []*gqlField
	for p.pos < len(p.tokens) && p.peek() != "}" {
		if p.peek() == "..." {
			p.pos++
			if strings.EqualFold(p.peek(), "on") {
				p.pos++
				if p.pos < len(p.tokens) && p.peek() != "{" {
					p.pos++ // type condition
				}
				for p.peek() == "@" {
					p.pos++
					if p.pos < len(p.tokens) {
						p.pos++
					}
					for p.peek() == "(" {
						p.skipBalanced("(", ")")
					}
				}
				if p.peek() == "{" {
					out = append(out, p.parseSelection()...)
				}
			} else {
				// Fragment spread: resolve it after all fragment definitions have
				// been scanned. Forward references are supported.
				name := p.peek()
				if p.pos < len(p.tokens) {
					p.pos++
				}
				for p.peek() == "@" {
					p.pos++
					if p.pos < len(p.tokens) {
						p.pos++
					}
					for p.peek() == "(" {
						p.skipBalanced("(", ")")
					}
				}
				out = append(out, &gqlField{Spread: name})
			}
			continue
		}
		name := p.peek()
		if name == "" || name == "}" {
			break
		}
		p.pos++
		response := name
		if p.peek() == ":" {
			p.pos++
			response = name
			if p.pos < len(p.tokens) {
				name = p.peek()
				p.pos++
			}
		}
		for p.peek() == "(" {
			p.skipBalanced("(", ")")
		}
		for p.peek() == "@" {
			p.pos++
			if p.pos < len(p.tokens) {
				p.pos++
			}
			for p.peek() == "(" {
				p.skipBalanced("(", ")")
			}
		}
		var children []*gqlField
		if p.peek() == "{" {
			children = p.parseSelection()
		}
		out = append(out, &gqlField{ResponseName: response, Name: name, Children: children})
	}
	if p.peek() == "}" {
		p.pos++
	}
	return out
}

func expandFields(fields []*gqlField, fragments map[string][]*gqlField, seen map[string]bool) []*gqlField {
	var out []*gqlField
	for _, f := range fields {
		if f.Spread != "" {
			if seen[f.Spread] {
				continue
			}
			seen[f.Spread] = true
			out = append(out, expandFields(fragments[f.Spread], fragments, seen)...)
			delete(seen, f.Spread)
			continue
		}
		f.Children = expandFields(f.Children, fragments, seen)
		out = append(out, f)
	}
	return out
}

// inferQueryColumns extracts selected fields from a configured GraphQL query.
// It is intentionally best-effort: schema hints from the query are better than
// an empty table, and Fetch still infers columns from actual rows.
func inferQueryColumns(query, dataPath string) []connector.Column {
	fields := (&gqlParser{tokens: lexGraphQL(query)}).parseDocument()
	if len(fields) == 0 {
		return nil
	}
	path := strings.Split(normalizePath(dataPath), ".")
	if strings.TrimSpace(dataPath) == "" {
		path = nil
	}
	var selected *gqlField
	if len(path) > 0 {
		selected = findField(fields, path[0])
		for _, part := range path[1:] {
			if selected == nil {
				return nil
			}
			selected = findField(selected.Children, part)
		}
	} else {
		for _, f := range fields {
			if f.ResponseName == "__typename" {
				continue
			}
			if selected == nil {
				selected = f
			}
		}
	}
	if selected == nil {
		return nil
	}
	// Auto-unwrap common GraphQL connection wrappers when no explicit path was
	// configured.
	if len(path) == 0 {
		selected = unwrapConnection(selected)
	}
	return columnFields(selected.Children)
}

func unwrapConnection(field *gqlField) *gqlField {
	if field == nil {
		return nil
	}
	if nodes := findField(field.Children, "nodes"); nodes != nil && len(nodes.Children) > 0 {
		return nodes
	}
	if edges := findField(field.Children, "edges"); edges != nil {
		if node := findField(edges.Children, "node"); node != nil {
			return node
		}
		return edges
	}
	if len(field.Children) == 1 && len(field.Children[0].Children) > 0 {
		return field.Children[0]
	}
	return field
}

func findField(fields []*gqlField, name string) *gqlField {
	for _, f := range fields {
		if strings.EqualFold(f.ResponseName, name) || strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

func columnFields(fields []*gqlField) []connector.Column {
	seen := map[string]bool{}
	var out []connector.Column
	for _, f := range fields {
		name := f.ResponseName
		if name == "" || name == "__typename" || strings.EqualFold(name, "pageInfo") || strings.EqualFold(name, "totalCount") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		typ := "string"
		if len(f.Children) > 0 {
			typ = "object"
		}
		out = append(out, connector.Column{Name: name, Type: typ, Nullable: true})
	}
	return out
}

// inferRelayPagination detects the common Relay connection shape:
//
//	issues(first: 100, after: $after) {
//	  nodes { ... }
//	  pageInfo { hasNextPage endCursor }
//	}
//
// It is best-effort and only fills unset pagination fields. APIs with a
// different cursor argument or pageInfo shape can still configure them
// explicitly.
func inferRelayPagination(query, dataPath string) (cursorVar, cursorPath, hasNextPath string) {
	fields := (&gqlParser{tokens: lexGraphQL(query)}).parseDocument()
	if len(fields) == 0 {
		return "", "", ""
	}
	path := strings.Split(normalizePath(dataPath), ".")
	if strings.TrimSpace(dataPath) == "" {
		path = nil
	}
	root := fields[0]
	parentPath := root.ResponseName
	if len(path) > 0 {
		root = findField(fields, path[0])
		if root == nil {
			return "", "", ""
		}
		parentPath = root.ResponseName
		// pageInfo is a sibling of the connection's nodes/edges list.
		if len(path) > 1 && (strings.EqualFold(path[len(path)-1], "nodes") || strings.EqualFold(path[len(path)-1], "edges")) {
			// parentPath already points at the connection field.
		}
	}
	pageInfo := findField(root.Children, "pageInfo")
	if pageInfo == nil {
		return "", "", ""
	}
	has := findField(pageInfo.Children, "hasNextPage") != nil
	end := findField(pageInfo.Children, "endCursor") != nil
	if !has && !end {
		return "", "", ""
	}
	cursorVar = relayCursorVariable(query)
	if cursorVar == "" {
		return "", "", ""
	}
	if end {
		cursorPath = parentPath + ".pageInfo.endCursor"
	}
	if has {
		hasNextPath = parentPath + ".pageInfo.hasNextPage"
	}
	return cursorVar, cursorPath, hasNextPath
}

func relayCursorVariable(query string) string {
	tokens := lexGraphQL(query)
	var names []string
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].text == "$" {
			names = append(names, tokens[i+1].text)
		}
	}
	for _, want := range []string{"after", "cursor", "endCursor"} {
		for _, name := range names {
			if strings.EqualFold(name, want) {
				return name
			}
		}
	}
	return ""
}
