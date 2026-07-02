package rest

import "net/http"

// CORSMiddleware allows the Web Console to call this API cross-origin.
// See services/query-api/internal/rest/cors.go for the full rationale
// (identical here — both are browser-facing REST APIs authenticated by a
// static API-key header, not cookies).
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-AgentMesh-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
