// Package app wires configuration, secrets, storage, contracts and HTTP
// routes into a runnable Nexus server.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/catalog"
	"github.com/ntsrihari/knowhere-nexus/internal/config"
	"github.com/ntsrihari/knowhere-nexus/internal/contract"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/secrets"
	"github.com/ntsrihari/knowhere-nexus/internal/store"
	"github.com/ntsrihari/knowhere-nexus/internal/webui"
)

// allMethods is used by catch-all routes that answer every method.
var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost,
	http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// App is a fully initialised Nexus instance.
type App struct {
	cfg     *config.Config
	log     *slog.Logger
	db      *store.DB
	secrets *secrets.Secrets
	catalog *catalog.Holder
	theme   *contract.Theme
	routes  *authz.Registry
	site    *webui.Site
	handler http.Handler
	limits  httpx.Limits
}

// Options adjust construction (tests use them).
type Options struct {
	Limits *httpx.Limits
	// Static replaces the embedded frontend build (tests).
	Static fs.FS
}

// New initialises everything that must be valid before the server listens.
// Any error means Nexus must not start.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger, opt Options) (*App, error) {
	sec, err := secrets.Load(cfg.SecretsFile)
	if err != nil {
		return nil, err
	}
	if err := contract.SelfCheck(); err != nil {
		return nil, fmt.Errorf("contract schemas: %w", err)
	}
	theme, err := contract.DefaultTheme()
	if err != nil {
		return nil, fmt.Errorf("default theme: %w", err)
	}
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:     cfg,
		log:     log,
		db:      db,
		secrets: sec,
		catalog: catalog.NewHolder(),
		theme:   theme,
		limits:  httpx.DefaultLimits,
	}
	if opt.Limits != nil {
		a.limits = *opt.Limits
	}
	static := opt.Static
	if static == nil {
		static = webui.Embedded()
	}
	if a.site, err = webui.New(static); err != nil {
		db.Close()
		return nil, fmt.Errorf("frontend: %w", err)
	}
	if !a.site.Built() {
		log.Warn("frontend build not embedded; pages will answer 503 (run make frontend before go build)")
	}
	// No route registry exists yet (R-06, B-07): no catalog route is authorized.
	a.catalog.LoadAtStartup(cfg.CatalogPath, contract.CatalogOptions{}, log)

	a.routes = authz.NewRegistry()
	a.registerRoutes()
	mux, err := a.routes.Build(authz.PublicOnly{})
	if err != nil {
		db.Close()
		return nil, err
	}
	a.handler = httpx.Chain(mux,
		httpx.SecurityHeaders(a.csp()),
		httpx.RequestID(),
		httpx.AccessLog(log),
		httpx.Recover(log),
		httpx.LimitBody(a.limits.MaxBodyBytes),
	)
	return a, nil
}

func (a *App) registerRoutes() {
	a.routes.Handle("/healthz", authz.Public, []string{http.MethodGet}, http.HandlerFunc(health))
	a.routes.Handle("/api/", authz.Public, allMethods, http.HandlerFunc(apiNotFound))
	a.routes.Handle("/", authz.Public, []string{http.MethodGet}, a.site)
}

func health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "not found")
}

// cspDirectives is the Content-Security-Policy (SECURITY.md §4.7). script-src
// gains the hashes of index.html's inline bootstrap script; style-src-attr
// allows only the exact inline style attributes listed in allowedStyleAttrs.
const cspDirectives = "default-src 'self'; script-src 'self'%s; style-src 'self'; style-src-attr %s; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'none'; " +
	"frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'"

// allowedStyleAttrs are the only inline style attributes the page may use.
// SvelteKit's route announcer (an accessibility live region that every
// SvelteKit app renders) carries this fixed style. If a SvelteKit upgrade
// changes it, the browser CSP check (make e2e) fails.
var allowedStyleAttrs = []string{
	"position: absolute; left: 0; top: 0; clip: rect(0 0 0 0); clip-path: inset(50%); overflow: hidden; white-space: nowrap; width: 1px; height: 1px",
}

func styleAttrSources() string {
	if len(allowedStyleAttrs) == 0 {
		return "'none'"
	}
	parts := []string{"'unsafe-hashes'"}
	for _, s := range allowedStyleAttrs {
		sum := sha256.Sum256([]byte(s))
		parts = append(parts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return strings.Join(parts, " ")
}

func (a *App) csp() string {
	extra := ""
	if h := a.site.ScriptHashes(); len(h) > 0 {
		extra = " " + strings.Join(h, " ")
	}
	return fmt.Sprintf(cspDirectives, extra, styleAttrSources())
}

// Handler returns the complete HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Routes returns the permission table (for tests and diagnostics).
func (a *App) Routes() []authz.Info { return a.routes.Routes() }

// Catalog returns the catalog holder.
func (a *App) Catalog() *catalog.Holder { return a.catalog }

// Close releases resources.
func (a *App) Close() error { return a.db.Close() }

// Serve listens on the configured address until ctx is cancelled, then shuts
// down gracefully.
func (a *App) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.cfg.Listen, err)
	}
	return a.ServeListener(ctx, ln)
}

// ServeListener serves on ln until ctx is cancelled.
func (a *App) ServeListener(ctx context.Context, ln net.Listener) error {
	srv := httpx.NewServer(a.cfg.Listen, a.handler, a.limits)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	a.log.Info("nexus listening", "addr", ln.Addr().String(), "origin", a.cfg.PublicOrigin)
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.log.Info("shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}
