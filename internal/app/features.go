package app

import (
	"context"
	"net/http"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

// registerFeatureRoutes adds the M3+ routes (widgets, launcher, themes,
// integrations, service tokens). Filled in by later milestones.
func (a *App) registerFeatureRoutes() {}

// widgetSet holds cached widget values (M3).
type widgetSet struct{ a *App }

func newWidgetSet(a *App) *widgetSet           { return &widgetSet{a: a} }
func (w *widgetSet) start(context.Context)     {}
func (w *widgetSet) snapshot(admin bool) []sse { return nil }

// serviceGuard authenticates service tokens (R-03); added with Collector support.
func (a *App) serviceGuard(role authz.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "service authentication required")
	})
}
