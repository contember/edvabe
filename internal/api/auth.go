package api

import "net/http"

// RequireAPIKey accepts any non-empty X-API-Key and rejects missing keys
// with the standard E2B error envelope.
func RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "" {
			WriteError(w, http.StatusUnauthorized, "missing X-API-Key header")
			return
		}
		next.ServeHTTP(w, r)
	})
}
