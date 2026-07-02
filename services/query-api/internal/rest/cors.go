package rest

import "net/http"

// CORSMiddleware allows the Web Console (a browser SPA served from its own
// origin, per Repository Structure.md's web/console/) to call this REST
// API cross-origin. Architecture.md §11 names the Web Console as a client
// of this API; a browser enforces CORS regardless of API-key auth being
// otherwise correct, so without this every browser-originated request
// fails before the request even reaches authz.Middleware — caught by
// actually loading the Console against a live Query API, not by
// inspection.
//
// Allowing any origin ("*") is acceptable here specifically because this
// API is authenticated by a static bearer-style API key header, not
// cookies — CORS's credentialed-request restrictions (which "*" cannot
// satisfy) exist to protect cookie-based auth from cross-site reads, a
// concern that does not apply to a header-based key the browser must be
// explicitly told to send.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-AgentMesh-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
