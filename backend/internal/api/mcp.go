package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/cryptox"
	"github.com/anyquery/anyquery/internal/engine"
	"github.com/anyquery/anyquery/internal/model"
	"github.com/anyquery/anyquery/internal/store"
)

// authenticatePAT is used by /mcp. MCP deliberately accepts only personal
// access tokens, never browser session cookies.
func (s *Server) authenticatePAT(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer aq_") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="anyquery-mcp"`)
			writeError(w, http.StatusUnauthorized, "pat_required", "MCP requires a personal access token (Authorization: Bearer aq_…)")
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		key, err := s.store.GetAPIKeyByHash(r.Context(), cryptox.HashToken(raw))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_pat", "Personal access token is invalid or revoked")
			return
		}
		user, err := s.store.GetUser(r.Context(), key.CreatedBy)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_pat", "Token owner no longer exists")
			return
		}
		tenant, err := s.store.GetTenant(r.Context(), key.TenantID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_pat", "Workspace no longer exists")
			return
		}
		s.store.TouchAPIKey(r.Context(), key.ID)
		ctx := context.WithValue(r.Context(), principalKey, &principal{User: user, APIKey: key})
		ctx = context.WithValue(ctx, tenantKey, tenant)
		ctx = context.WithValue(ctx, roleKey, model.RoleMember)
		next(w, r.WithContext(ctx))
	}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	scope       string
}

var mcpTools = []mcpTool{
	{
		Name:        "get_catalog",
		Description: "List data sources, tables and columns available to this workspace.",
		scope:       model.ScopeCatalogRead,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{"type": "string", "description": "Optional data source name"},
			},
		},
	},
	{
		Name:        "execute_sql",
		Description: "Execute federated AnyQuery SQL. Reference tables as `source`.`table`.",
		scope:       model.ScopeQueryExecute,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sql"},
			"properties": map[string]any{
				"sql":   map[string]any{"type": "string", "description": "SELECT/SHOW query to execute"},
				"limit": map[string]any{"type": "integer", "description": "Maximum rows returned to the model (default 200, max 1000)"},
			},
		},
	},
	{
		Name:        "list_saved_queries",
		Description: "List saved queries visible to this token's owner.",
		scope:       model.ScopeQueryRead,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req mcpRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := dec.Decode(&req); err != nil {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		if !notification {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32600, Message: "invalid JSON-RPC version"}})
		}
		return
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if notification {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2025-03-26"
		}
		session, _ := cryptox.RandomToken(16)
		w.Header().Set("Mcp-Session-Id", session)
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "AnyQuery", "version": "0.1.0"},
		}})
	case "ping":
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		var tools []map[string]any
		for _, tool := range mcpTools {
			if !hasScope(r, tool.scope) {
				continue
			}
			tools = append(tools, map[string]any{
				"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema,
			})
		}
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32602, Message: "invalid tool call parameters"}})
			return
		}
		var tool *mcpTool
		for i := range mcpTools {
			if mcpTools[i].Name == params.Name {
				tool = &mcpTools[i]
				break
			}
		}
		if tool == nil {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32601, Message: "unknown tool " + params.Name}})
			return
		}
		if !hasScope(r, tool.scope) {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: toolError(fmt.Sprintf("This token is missing the %s scope", tool.scope))})
			return
		}
		result, err := s.callMCPTool(r, tool.Name, params.Arguments)
		if err != nil {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: toolError(err.Error())})
			return
		}
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	default:
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32601, Message: "method not found"}})
	}
}

func writeMCP(w http.ResponseWriter, resp mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func toolText(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

func toolError(text string) map[string]any {
	result := toolText(text)
	result["isError"] = true
	return result
}

func (s *Server) callMCPTool(r *http.Request, name string, rawArgs json.RawMessage) (map[string]any, error) {
	tenant := currentTenant(r)
	user := currentUser(r)
	switch name {
	case "get_catalog":
		var args struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		list, err := s.store.ListDataSources(r.Context(), tenant.ID)
		if err != nil {
			return nil, err
		}
		type sourceOut struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Status string            `json:"status"`
			Tables []connector.Table `json:"tables"`
		}
		var out []sourceOut
		for _, ds := range list {
			if args.Source != "" && !strings.EqualFold(ds.Name, args.Source) {
				continue
			}
			out = append(out, sourceOut{Name: ds.Name, Type: ds.Type, Status: ds.Status, Tables: ds.Tables})
		}
		return toolText(mustJSON(map[string]any{"sources": out})), nil
	case "execute_sql":
		var args struct {
			SQL    string `json:"sql"`
			Limit  int    `json:"limit"`
			Params []any  `json:"params"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		sqlText := strings.TrimSpace(args.SQL)
		if sqlText == "" {
			return nil, fmt.Errorf("sql is required")
		}
		if len(sqlText) > 200_000 {
			return nil, fmt.Errorf("query text is too long")
		}
		res, run, err := s.executeForAPI(r.Context(), tenant, user.ID, sqlText, args.Params)
		if err != nil {
			return nil, err
		}
		rowLimit := args.Limit
		if rowLimit <= 0 {
			rowLimit = 200
		}
		if rowLimit > 1000 {
			rowLimit = 1000
		}
		rows := res.Rows
		truncated := res.Truncated
		if len(rows) > rowLimit {
			rows = rows[:rowLimit]
			truncated = true
		}
		return toolText(mustJSON(map[string]any{
			"runId": run.ID, "columns": res.Columns, "rows": rows,
			"rowCount": res.RowCount, "truncated": truncated, "warnings": res.Warnings,
			"stats": res.Stats,
		})), nil
	case "list_saved_queries":
		list, err := s.store.ListSavedQueries(r.Context(), tenant.ID, user.ID, false)
		if err != nil {
			return nil, err
		}
		return toolText(mustJSON(map[string]any{"queries": list})), nil
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) executeForAPI(ctx context.Context, tenant *model.Tenant, userID, sqlText string, params []any) (*engine.Result, *model.QueryRun, error) {
	cat := &tenantCatalog{server: s, tenantID: tenant.ID, opened: map[string]connector.Connector{}}
	defer cat.Close()
	res, execErr := s.engine.ExecuteParams(ctx, sqlText, params, cat)
	run := &model.QueryRun{
		ID: store.NewID(), TenantID: tenant.ID, UserID: userID, SQL: sqlText,
		CreatedAt: store.Now(), Sources: []string{},
	}
	if execErr != nil {
		run.Status, run.Error = "error", execErr.Error()
		_ = s.store.CreateRun(ctx, run)
		return nil, run, execErr
	}
	run.Status, run.RowCount, run.DurationMS = "success", res.RowCount, res.Stats.DurationMS
	seen := map[string]bool{}
	for _, st := range res.Stats.Sources {
		if !seen[st.Source] {
			seen[st.Source] = true
			run.Sources = append(run.Sources, st.Source)
		}
	}
	_ = s.store.CreateRun(ctx, run)
	return res, run, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"failed to encode result"}`
	}
	return string(b)
}

var _ = errors.Is
