// Package httpapi contient la couche HTTP : routes, handlers, middlewares, Problem Details.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const readinessPingTimeout = 2 * time.Second

// Pinger est satisfait par *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Health sert /healthz et /readyz (SPEC §7.3).
type Health struct {
	db           Pinger
	logger       *slog.Logger
	shuttingDown atomic.Bool
}

func NewHealth(db Pinger, logger *slog.Logger) *Health {
	return &Health{db: db, logger: logger}
}

// SetShuttingDown fait passer /readyz en 503 pendant l'arrêt gracieux.
func (h *Health) SetShuttingDown() {
	h.shuttingDown.Store(true)
}

func (h *Health) liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

func (h *Health) readiness(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Service unavailable", "Le serveur est en cours d'arrêt.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		h.logger.Warn("readiness check failed", "err", err)
		writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Service unavailable", "La base de données ne répond pas.")
		return
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}
