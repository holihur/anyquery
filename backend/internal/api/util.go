package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/anyquery/anyquery/internal/model"
)

func parseLimit(r *http.Request, def int64) int64 {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// hasScope allows browser sessions and legacy full-access keys, but enforces
// explicit PAT scopes when present.
func hasScope(r *http.Request, scope string) bool {
	p := currentPrincipal(r)
	if p == nil || p.APIKey == nil {
		return true
	}
	return p.APIKey.HasScope(scope)
}

func requireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if hasScope(r, scope) {
		return true
	}
	writeError(w, http.StatusForbidden, "scope_required", fmt.Sprintf("This token is missing the %s scope", scope))
	return false
}

func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return append([]string{}, model.AllScopes...), nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, scope := range in {
		scope = strings.TrimSpace(scope)
		if !model.ValidScope(scope) {
			return nil, fmt.Errorf("unknown scope %q", scope)
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one scope is required")
	}
	return out, nil
}
