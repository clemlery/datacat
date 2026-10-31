package httpapi

import "net/http"

// NewRouter construit le routeur de l'API. Les routes /api/v1 arrivent avec le CRUD.
func NewRouter(health *Health) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.liveness)
	mux.HandleFunc("GET /readyz", health.readiness)
	return mux
}
