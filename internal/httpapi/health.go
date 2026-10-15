package httpapi

import (
	"context"
	"net/http"
	"time"
)

// healthz — процесс жив.
func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz — процесс готов принимать запросы: БД отвечает.
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.DB.Ping(ctx); err != nil {
		a.Log.Warn("readiness check failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "Not ready", "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
