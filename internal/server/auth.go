package server

import (
	"net/http"
	"strings"
)

// TokenAuth provides simple bearer token authentication.
// If Token is empty, auth is disabled.
type TokenAuth struct {
	Token string
}

// Middleware returns an HTTP middleware that checks for a valid bearer token.
// For WebSocket upgrades, it checks the "token" query parameter.
func (a *TokenAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Token == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization header
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == a.Token {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Check query parameter (for WebSocket connections)
		if r.URL.Query().Get("token") == a.Token {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}
