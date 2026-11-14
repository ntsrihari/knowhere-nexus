// Package authz is the single, default-deny route permission table (P-15).
//
// Every route is registered with a Role and the HTTP methods it accepts. The
// mux is built only from this registry, so a handler cannot be reachable
// without a permission entry. Registering a route without a valid role is a
// programming error and panics; Build re-validates the whole table and the
// server refuses to start if anything is wrong.
package authz

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

// Role is the minimum caller role for a route (API_SPEC.md §0).
type Role string

const (
	Public           Role = "public"
	User             Role = "user"
	Admin            Role = "admin"
	AdminFresh       Role = "admin!"
	AdminFreshReauth Role = "admin!+reauth"
	servicePrefix         = "service:"
)

var scopeRe = regexp.MustCompile(`^[a-z][a-z0-9]*(:[a-z][a-z0-9-]*)+$`)

// Service returns the role for a service token with the given scope, for
// example Service("catalog:publish").
func Service(scope string) Role { return Role(servicePrefix + scope) }

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case Public, User, Admin, AdminFresh, AdminFreshReauth:
		return true
	}
	if s, ok := strings.CutPrefix(string(r), servicePrefix); ok {
		return scopeRe.MatchString(s)
	}
	return false
}

// Route is one entry of the permission table.
type Route struct {
	Pattern string // net/http ServeMux path pattern without a method, e.g. "/healthz"
	Methods []string
	Role    Role
	Handler http.Handler
}

// Info is the public, handler-free description of a route.
type Info struct {
	Pattern string
	Methods []string
	Role    Role
}

// Registry collects routes.
type Registry struct {
	routes []Route
	seen   map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{seen: map[string]bool{}} }

var knownMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete,
}

// Handle registers a route. It panics on an invalid role, pattern, method
// list or handler, or on a duplicate pattern.
func (reg *Registry) Handle(pattern string, role Role, methods []string, h http.Handler) {
	r := Route{Pattern: pattern, Methods: methods, Role: role, Handler: h}
	if err := r.validate(); err != nil {
		panic("authz: " + err.Error())
	}
	if reg.seen[pattern] {
		panic("authz: duplicate route " + pattern)
	}
	reg.seen[pattern] = true
	reg.routes = append(reg.routes, r)
}

func (r Route) validate() error {
	if !strings.HasPrefix(r.Pattern, "/") || strings.ContainsAny(r.Pattern, " \t") {
		return fmt.Errorf("route %q: pattern must be a path without a method", r.Pattern)
	}
	if !r.Role.Valid() {
		return fmt.Errorf("route %q: missing or unknown role %q", r.Pattern, r.Role)
	}
	if len(r.Methods) == 0 {
		return fmt.Errorf("route %q: no methods", r.Pattern)
	}
	for _, m := range r.Methods {
		if !slices.Contains(knownMethods, m) {
			return fmt.Errorf("route %q: unknown method %q", r.Pattern, m)
		}
	}
	if r.Handler == nil {
		return fmt.Errorf("route %q: nil handler", r.Pattern)
	}
	return nil
}

// Routes returns the table, sorted by pattern.
func (reg *Registry) Routes() []Info {
	out := make([]Info, 0, len(reg.routes))
	for _, r := range reg.routes {
		out = append(out, Info{Pattern: r.Pattern, Methods: slices.Clone(r.Methods), Role: r.Role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })
	return out
}

// Guard enforces a role in front of a handler.
type Guard interface {
	Enforce(role Role, next http.Handler) http.Handler
}

// Build validates every route and returns the mux. Any invalid entry, or a
// pattern the ServeMux rejects, is an error: the server must not start.
func (reg *Registry) Build(g Guard) (http.Handler, error) {
	if g == nil {
		return nil, errors.New("authz: nil guard")
	}
	if len(reg.routes) == 0 {
		return nil, errors.New("authz: no routes registered")
	}
	mux := http.NewServeMux()
	for _, r := range reg.routes {
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("authz: %w", err)
		}
		h := methods(r.Methods, g.Enforce(r.Role, r.Handler))
		if err := safeHandle(mux, r.Pattern, h); err != nil {
			return nil, fmt.Errorf("authz: route %q: %v", r.Pattern, err)
		}
	}
	return mux, nil
}

func safeHandle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%v", v)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// methods rejects methods not listed for the route with a 405 envelope.
// GET implies HEAD.
func methods(allowed []string, next http.Handler) http.Handler {
	allow := slices.Clone(allowed)
	if slices.Contains(allow, http.MethodGet) && !slices.Contains(allow, http.MethodHead) {
		allow = append(allow, http.MethodHead)
	}
	header := strings.Join(allow, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(allow, r.Method) {
			w.Header().Set("Allow", header)
			httpx.WriteError(w, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "method not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PublicOnly is the M1 guard: public routes pass, every other role is denied
// with 401 because no authentication exists yet. M2 replaces it with the
// session-aware guard.
type PublicOnly struct{}

// Enforce implements Guard.
func (PublicOnly) Enforce(role Role, next http.Handler) http.Handler {
	if role == Public {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "authentication required")
	})
}
