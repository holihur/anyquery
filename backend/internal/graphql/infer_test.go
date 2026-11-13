package graphql

import "testing"

func TestInferQueryColumns(t *testing.T) {
	tests := []struct {
		name  string
		query string
		path  string
		want  []string
	}{
		{"simple", `query { users { id name email } }`, "", []string{"id", "name", "email"}},
		{"alias", `query { u: users { id: userId name } }`, "", []string{"id", "name"}},
		{"connection", `query { issues(first: 10) { nodes { id title } pageInfo { hasNextPage } } }`, "", []string{"id", "title"}},
		{"edges", `query { orders { edges { node { id total } } } }`, "", []string{"id", "total"}},
		{"path", `query { orders { nodes { id } } }`, "orders.nodes", []string{"id"}},
		{"nested object", `query { users { id profile { city } } }`, "", []string{"id", "profile"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols := inferQueryColumns(tt.query, tt.path)
			if len(cols) != len(tt.want) {
				t.Fatalf("columns = %+v, want %v", cols, tt.want)
			}
			for i, name := range tt.want {
				if cols[i].Name != name {
					t.Fatalf("columns = %+v, want %v", cols, tt.want)
				}
			}
		})
	}
}

func TestGraphQLInfersColumnsOnOpen(t *testing.T) {
	c, err := (provider{}).Open(t.Context(), map[string]string{
		"endpoint": "https://example.com/graphql",
		"query":    `query { users { id name } }`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := c.Introspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || len(tables[0].Columns) != 2 {
		t.Fatalf("tables = %+v", tables)
	}
}
