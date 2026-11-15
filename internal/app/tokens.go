package app

import (
	"net/http"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

// serviceAuth is replaced by the service-token implementation (R-03).
func (a *App) serviceAuth(role authz.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "service authentication required")
	})
}
