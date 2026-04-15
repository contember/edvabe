package api

import (
	"encoding/json"
	"net/http"
)

// WriteError writes the E2B-compatible control-plane error envelope.
func WriteError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": message,
	})
}
