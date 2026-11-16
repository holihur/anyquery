package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/anyquery/anyquery/internal/connector"
	"github.com/anyquery/anyquery/internal/engine"
	"github.com/anyquery/anyquery/internal/model"
)

const dashboardRunConcurrency = 6

type dashboardPanelRun struct {
	ID         string         `json:"id"`
	Result     *engine.Result `json:"result,omitempty"`
	Error      *apiError      `json:"error,omitempty"`
	DurationMS int64          `json:"durationMs"`
	CacheHit   bool           `json:"cacheHit,omitempty"`
}

// handleDashboardRun executes all panels in one request with bounded
// server-side concurrency. It never writes query history; dashboard refreshes
// are snapshots rather than user-authored query runs. The per-tenant query
// cache and singleflight still apply.
func (s *Server) handleDashboardRun(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, model.ScopeQueryExecute) {
		return
	}
	t := currentTenant(r)
	if !currentRole(r).CanQuery() {
		writeError(w, http.StatusForbidden, "forbidden", "You do not have query access")
		return
	}
	d, err := s.store.GetDashboard(r.Context(), t.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Dashboard not found")
		return
	}
	out := make([]dashboardPanelRun, len(d.Panels))
	if len(d.Panels) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"dashboardId": d.ID, "panels": out})
		return
	}
	sem := make(chan struct{}, minInt(dashboardRunConcurrency, len(d.Panels)))
	var wg sync.WaitGroup
	for i, panel := range d.Panels {
		wg.Add(1)
		go func(index int, panel model.DashboardPanel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			start := time.Now()
			cat := &tenantCatalog{server: s, tenantID: t.ID, opened: map[string]connector.Connector{}}
			defer cat.Close()
			ctx := r.Context()
			var result *engine.Result
			var cacheHit bool
			var execErr error
			if s.queryCache.Enabled() {
				result, cacheHit, execErr = s.queryCache.GetOrLoad(ctx, queryCacheKey(t.ID, panel.SQL, nil), func() (*engine.Result, error) {
					return s.engine.ExecuteParams(ctx, panel.SQL, nil, cat)
				})
			} else {
				result, execErr = s.engine.ExecuteParams(ctx, panel.SQL, nil, cat)
			}
			out[index] = dashboardPanelRun{
				ID:         panel.ID,
				Result:     result,
				DurationMS: time.Since(start).Milliseconds(),
				CacheHit:   cacheHit,
			}
			if execErr != nil {
				out[index].Error = queryAPIError(execErr)
			}
		}(i, panel)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"dashboardId": d.ID, "panels": out})
}

func queryAPIError(err error) *apiError {
	var qe *engine.Error
	if errors.As(err, &qe) {
		return &apiError{Code: qe.Code, Message: qe.Message, Hint: qe.Hint}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &apiError{Code: "timeout", Message: "Query timeout"}
	}
	return &apiError{Code: "execution_error", Message: err.Error()}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
