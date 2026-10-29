package httpapi

import (
	"encoding/json"
	"net/http"
)

const problemTypeBase = "https://datacat.local/problems/"

// Problem est une réponse d'erreur RFC 9457 (SPEC §5.10).
// request_id est ajouté avec le middleware de request ID.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, typeSuffix, title, detail string) {
	writeJSON(w, status, "application/problem+json", Problem{
		Type:     problemTypeBase + typeSuffix,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: r.URL.Path,
	})
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	// Les en-têtes sont partis : une erreur d'écriture ne peut plus être signalée au client.
	_ = json.NewEncoder(w).Encode(v)
}
