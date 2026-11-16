package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

			out[index] = s.runDashboardPanel(r.Context(), t, panel)
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

func (s *Server) runDashboardPanel(ctx context.Context, t *model.Tenant, panel model.DashboardPanel) dashboardPanelRun {
	start := time.Now()
	cat := &tenantCatalog{server: s, tenantID: t.ID, opened: map[string]connector.Connector{}}
	defer cat.Close()
	result, cacheHit, execErr := s.executeCached(ctx, t.ID, panel.SQL, nil, cat)
	elapsed := time.Since(start)
	scanned := 0
	if result != nil {
		scanned = result.Stats.Scanned
	}
	s.metrics.Observe(t.ID, elapsed, scanned, cacheHit, execErr != nil)
	run := dashboardPanelRun{
		ID:         panel.ID,
		Result:     result,
		DurationMS: elapsed.Milliseconds(),
		CacheHit:   cacheHit,
	}
	if execErr != nil {
		run.Error = queryAPIError(execErr)
	}
	return run
}

func (s *Server) handleDashboardRunStream(w http.ResponseWriter, r *http.Request) {
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
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "Streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if len(d.Panels) == 0 {
		writeSSE(w, "done", map[string]any{"dashboardId": d.ID, "count": 0})
		flusher.Flush()
		return
	}
	ctx := r.Context()
	sem := make(chan struct{}, minInt(dashboardRunConcurrency, len(d.Panels)))
	results := make(chan dashboardPanelRun, len(d.Panels))
	var wg sync.WaitGroup
	for _, panel := range d.Panels {
		wg.Add(1)
		go func(panel model.DashboardPanel) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			result := s.runDashboardPanel(ctx, t, panel)
			select {
			case results <- result:
			case <-ctx.Done():
			}
		}(panel)
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	count := 0
	for result := range results {
		writeSSE(w, "panel", result)
		flusher.Flush()
		count++
	}
	if ctx.Err() == nil {
		writeSSE(w, "done", map[string]any{"dashboardId": d.ID, "count": count})
		flusher.Flush()
	}
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
