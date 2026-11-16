package api

import "net/http"

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !currentRole(r).CanAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "Admin access required")
		return
	}
	t := currentTenant(r)
	mode := "local"
	if s.sharedCache != nil && s.sharedCache.Enabled() {
		mode = "redis"
	}
	idle := 0
	if s.connectorPool != nil {
		idle = s.connectorPool.Len()
	}
	cache := map[string]any{
		"enabled":        s.queryCache.Enabled(),
		"entries":        s.queryCache.Len(),
		"bytes":          s.queryCache.Bytes(),
		"mode":           mode,
		"idleConnectors": idle,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": s.metrics.Tenant(t.ID),
		"cache":   cache,
		"scope":   "tenant",
	})
}
