package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerAuth returns middleware that requires `Authorization: Bearer <secret>`
// on mutating methods (POST/PUT/PATCH/DELETE). GET stays open so health/UI
// probes don't need tokens — Task 33 hardens routes individually if needed.
//
// secret == "" disables auth entirely; useful for local dev where the
// agent_mem.env hasn't been edited. A warning should be logged at startup
// in that case (main.go emits it via config.Validate).
func BearerAuth(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if secret == "" || !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			authHdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHdr, "Bearer ") {
				WriteError(w, http.StatusUnauthorized, "unauthorized", "Bearer token required")
				return
			}
			token := strings.TrimPrefix(authHdr, "Bearer ")
			// constant-time compare to avoid timing leaks
			if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
				WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
