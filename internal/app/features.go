package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/buildinfo"
	"github.com/ntsrihari/knowhere-nexus/internal/catalog"
	"github.com/ntsrihari/knowhere-nexus/internal/contract"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

func jsonBytes(v any) ([]byte, error) { return json.Marshal(v) }

// registerFeatureRoutes adds the M3–M5 routes.
func (a *App) registerFeatureRoutes() {
	get, post, put := []string{"GET"}, []string{"POST"}, []string{"PUT"}
	R := a.routes.Handle
	// Widgets (M3; A-07 user views, A-08 admin views).
	R("/api/v1/widgets/system", authz.User, get, a.widgetHandler("system", false))
	R("/api/v1/widgets/storage", authz.User, get, a.widgetHandler("storage", false))
	R("/api/v1/widgets/services", authz.User, get, a.widgetHandler("services", false))
	R("/api/v1/admin/widgets/storage", authz.Admin, get, a.widgetHandler("storage", true))
	R("/api/v1/admin/widgets/services", authz.Admin, get, a.widgetHandler("services", true))
	R("/api/v1/admin/storage/recheck", authz.Admin, post, http.HandlerFunc(a.handleStorageRecheck))
	R("/api/v1/admin/status", authz.Admin, get, http.HandlerFunc(a.handleStatus))
	// Launcher (M4; A-09, A-02).
	R("/api/v1/apps", authz.User, get, http.HandlerFunc(a.handleApps))
	R("/api/v1/admin/catalog/reload", authz.AdminFresh, post, http.HandlerFunc(a.handleCatalogReload))
	// Me and themes (M5).
	R("/api/v1/me", authz.User, get, http.HandlerFunc(a.handleMe))
	R("/api/v1/me/home-layout", authz.User, put, http.HandlerFunc(a.handleHomeLayout))
	R("/api/v1/me/theme", authz.User, put, http.HandlerFunc(a.handleMyTheme))
	R("/api/v1/themes", authz.User, get, http.HandlerFunc(a.handleThemes))
	R("/api/v1/themes/household", authz.User, get, http.HandlerFunc(a.handleHouseholdTheme))
	R("/api/v1/themes/household/set", authz.AdminFresh, put, http.HandlerFunc(a.handleSetHouseholdTheme))
	R("/api/v1/themes/effective", authz.User, get, http.HandlerFunc(a.handleEffectiveTheme))
	a.registerIntegrationRoutes()
	a.registerServiceRoutes()
}

// widgetHandler resolves the widget at request time.
func (a *App) widgetHandler(name string, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.widgets.handler(name, admin)(w, r) }
}

// integrationWidgets returns widgets for optional integrations (see integrations.go).
func (a *App) integrationWidgets() []*widget { return a.integrationWidgetList() }

func (a *App) handleStorageRecheck(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	if !a.writeAudit(w, r, a.entry(r, &s, "storage.recheck", "storage", "ok", nil)) {
		return
	}
	a.widgets.poke("storage")
	w.WriteHeader(http.StatusAccepted)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	ups := []upstream.Status{a.jf.Upstream().Status(), a.widgets.docker.Upstream().Status()}
	for _, c := range a.integrationClients() {
		ups = append(ups, c.Status())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": buildinfo.Version, "commit": buildinfo.Commit,
		"integrations": ups, "eventStreams": a.events.count()})
}

// --- launcher ---------------------------------------------------------------

type appView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Icon        string  `json:"icon"`
	Href        *string `json:"href"`
	Available   bool    `json:"available"`
}

func (a *App) routeURL(id string) (string, bool) {
	for _, rt := range a.cfg.Routes {
		if rt.ID == id {
			return rt.URL, true
		}
	}
	return "", false
}

func (a *App) handleApps(w http.ResponseWriter, r *http.Request) {
	admin := mustSession(r).IsAdmin(a.now())
	out := []appView{}
	for _, app := range a.catalog.Get().Apps {
		if app.Kind != "server" || (app.Audience == "admin" && !admin) {
			continue
		}
		v := appView{ID: app.ID, Name: app.Name, Description: app.Description, Icon: app.Icon.Builtin}
		if app.Launch != nil && app.Launch.Route != nil {
			if u, ok := a.routeURL(*app.Launch.Route); ok {
				v.Href, v.Available = &u, true
			}
		}
		out = append(out, v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) catalogOptions() contract.CatalogOptions {
	known := map[string]bool{}
	for _, rt := range a.cfg.Routes {
		known[rt.ID] = true
	}
	return contract.CatalogOptions{KnownRoutes: known}
}

func (a *App) handleCatalogReload(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	c, err := catalog.ReadFile(a.cfg.CatalogPath, a.catalogOptions())
	if err == nil {
		err = a.catalog.Replace(c)
	}
	if err != nil {
		_ = a.audit.Write(r.Context(), a.entry(r, &s, "catalog.reload", a.cfg.CatalogPath, "rejected", map[string]any{"error": err.Error()}))
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, err.Error())
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "catalog.reload", a.cfg.CatalogPath, "ok", map[string]any{"revision": c.Revision})) {
		return
	}
	a.events.publish("catalog.changed", map[string]int64{"revision": c.Revision}, false)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"revision": c.Revision, "schemaVersion": c.SchemaVersion, "apps": len(c.Apps)})
}

// --- me, preferences, themes -------------------------------------------------

func (a *App) pref(ctx context.Context, user, col string) (json.RawMessage, error) {
	var v sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT `+col+` FROM user_prefs WHERE user_id = ?`, user).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || !v.Valid {
		return nil, nil
	}
	return json.RawMessage(v.String), err
}

func (a *App) setPref(ctx context.Context, user, col string, val []byte) error {
	var v any
	if val != nil {
		v = string(val)
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO user_prefs (user_id, `+col+`, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET `+col+` = excluded.`+col+`, updated_at = excluded.updated_at`,
		user, v, a.now().UnixMilli())
	return err
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	theme, err1 := a.pref(r.Context(), s.UserID, "theme")
	layout, err2 := a.pref(r.Context(), s.UserID, "home_layout")
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "preferences unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": a.view(s).User, "role": s.EffectiveRole(a.now()),
		"prefs": map[string]any{"theme": theme, "homeLayout": layout}})
}

func readSmall(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "unreadable body")
		return nil, false
	}
	if int64(len(b)) > max {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "body too large")
		return nil, false
	}
	return b, true
}

func (a *App) handleHomeLayout(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	var body struct {
		Tiles   []string `json:"tiles"`
		Widgets []string `json:"widgets"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if len(body.Tiles) > 200 || len(body.Widgets) > 20 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "too many entries")
		return
	}
	known := map[string]bool{}
	for _, app := range a.catalog.Get().Apps {
		known[app.ID] = true
	}
	for _, t := range body.Tiles {
		if !known[t] {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "unknown tile "+t)
			return
		}
	}
	for _, wd := range body.Widgets {
		if a.widgets.byName[wd] == nil {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "unknown widget "+wd)
			return
		}
	}
	b, _ := json.Marshal(body)
	if err := a.setPref(r.Context(), s.UserID, "home_layout", b); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not save")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

func themeOptions() contract.ThemeOptions { return contract.ThemeOptions{Fonts: contract.DefaultFonts} }

// parseThemeBody accepts a knowhere.theme document or JSON null.
func parseThemeBody(w http.ResponseWriter, r *http.Request) (*contract.Theme, []byte, bool) {
	b, ok := readSmall(w, r, contract.ThemeMaxSize)
	if !ok {
		return nil, nil, false
	}
	if string(b) == "null" {
		return nil, nil, true
	}
	t, err := contract.ParseTheme(b, themeOptions())
	if err != nil {
		var ce *contract.Error
		code := httpx.CodeValidation
		if errors.As(err, &ce) && ce.Code == contract.CodeUnsupportedVersion {
			code = contract.CodeUnsupportedVersion
		}
		httpx.WriteError(w, http.StatusUnprocessableEntity, code, err.Error())
		return nil, nil, false
	}
	norm, _ := json.Marshal(t)
	return t, norm, true
}

func (a *App) handleMyTheme(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	t, norm, ok := parseThemeBody(w, r)
	if !ok {
		return
	}
	if err := a.setPref(r.Context(), s.UserID, "theme", norm); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not save")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (a *App) householdTheme(ctx context.Context) (*contract.Theme, error) {
	var v string
	err := a.db.QueryRowContext(ctx, `SELECT value FROM household WHERE key = 'theme'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.ParseTheme([]byte(v), themeOptions())
}

func (a *App) handleThemes(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, []map[string]any{{"id": "builtin:default", "name": a.theme.Name, "source": "builtin", "theme": a.theme}})
}

func (a *App) handleHouseholdTheme(w http.ResponseWriter, r *http.Request) {
	t, err := a.householdTheme(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "theme unavailable")
		return
	}
	if t == nil {
		t = a.theme
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (a *App) handleSetHouseholdTheme(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	t, norm, ok := parseThemeBody(w, r)
	if !ok {
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "theme.household.set", "household", "ok", nil)) {
		return
	}
	var err error
	if t == nil {
		_, err = a.db.ExecContext(r.Context(), `DELETE FROM household WHERE key = 'theme'`)
	} else {
		_, err = a.db.ExecContext(r.Context(), `INSERT INTO household (key, value, updated_by, updated_at) VALUES ('theme', ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			string(norm), s.UserID, a.now().UnixMilli())
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not save")
		return
	}
	a.events.publish("theme.changed", map[string]string{"scope": "household"}, false)
	if t == nil {
		t = a.theme
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// handleEffectiveTheme applies S-15 precedence (Material You is Milano-side).
func (a *App) handleEffectiveTheme(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	if raw, err := a.pref(r.Context(), s.UserID, "theme"); err == nil && raw != nil && string(raw) != "null" {
		if t, err := contract.ParseTheme(raw, themeOptions()); err == nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"theme": t, "from": "user"})
			return
		}
	}
	if t, err := a.householdTheme(r.Context()); err == nil && t != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"theme": t, "from": "household"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"theme": a.theme, "from": "builtin"})
}

// serviceGuard authenticates service tokens (R-03); see tokens.go.
func (a *App) serviceGuard(role authz.Role, next http.Handler) http.Handler {
	return a.serviceAuth(role, next)
}
