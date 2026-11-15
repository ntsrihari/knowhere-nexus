package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/config"
	"github.com/ntsrihari/knowhere-nexus/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunWithoutNetwork(m) }

const secretKeyByte = 0x5a

// testConfig writes a secrets file and returns a config for a temp data dir.
func testConfig(t *testing.T, catalogPath string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	sec := filepath.Join(dir, "secrets.yaml")
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{secretKeyByte}, 32))
	if err := os.WriteFile(sec, []byte("sessionEncryptionKey: "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if catalogPath == "" {
		catalogPath, _ = filepath.Abs("../../examples/catalog.yaml")
	}
	return &config.Config{
		TrustedProxies: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.0.2.1")},
		RequireTailnet: false,
		Listen:         "127.0.0.1:0",
		PublicOrigin:   "http://localhost:8480",
		DataDir:        filepath.Join(dir, "data"),
		CatalogPath:    catalogPath,
		SecretsFile:    sec,
		LogLevel:       slog.LevelInfo,
	}
}

func newApp(t *testing.T, cfg *config.Config) (*App, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	a, err := New(context.Background(), cfg, slog.New(slog.NewJSONHandler(&logs, nil)), Options{Static: testSite})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, &logs
}

// ST-1: the complete permission table. Any route added later must be added
// here with its role, or this test fails.
func TestRouteTable(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	get, post, del := []string{"GET"}, []string{"POST"}, []string{"DELETE"}
	want := []authz.Info{
		{Pattern: "/", Methods: get, Role: authz.Public},
		{Pattern: "/api/", Methods: allMethods, Role: authz.Public},
		{Pattern: "/api/v1/.well-known/jwks.json", Methods: get, Role: authz.Public},
		{Pattern: "/api/v1/admin/analytics", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/arr/calendar", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/arr/queue", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/audit", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/audit/verify", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/catalog/reload", Methods: post, Role: authz.AdminFresh},
		{Pattern: "/api/v1/admin/jellyfin/tasks", Methods: get, Role: authz.AdminFresh},
		{Pattern: "/api/v1/admin/jellyfin/tasks/{id}/run", Methods: post, Role: authz.AdminFresh},
		{Pattern: "/api/v1/admin/service-tokens/revoke", Methods: post, Role: authz.AdminFresh},
		{Pattern: "/api/v1/admin/services", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/services/{id}/disable", Methods: post, Role: authz.AdminFreshReauth},
		{Pattern: "/api/v1/admin/services/{id}/enable", Methods: post, Role: authz.AdminFreshReauth},
		{Pattern: "/api/v1/admin/sessions", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/sessions/{id}", Methods: del, Role: authz.AdminFresh},
		{Pattern: "/api/v1/admin/status", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/storage/recheck", Methods: post, Role: authz.Admin},
		{Pattern: "/api/v1/admin/users/{userId}/revoke-sessions", Methods: post, Role: authz.AdminFreshReauth},
		{Pattern: "/api/v1/admin/widgets/dns", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/downloads", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/indexers", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/now-playing", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/photos", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/services", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/admin/widgets/storage", Methods: get, Role: authz.Admin},
		{Pattern: "/api/v1/apps", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/auth/login", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/auth/logout", Methods: post, Role: authz.User},
		{Pattern: "/api/v1/auth/quickconnect", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/auth/quickconnect/{pollId}", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/auth/reauth", Methods: post, Role: authz.User},
		{Pattern: "/api/v1/auth/refresh", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/auth/session", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/auth/token-exchange", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/auth/verify", Methods: get, Role: authz.Public},
		{Pattern: "/api/v1/events", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/me", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/me/home-layout", Methods: []string{"PUT"}, Role: authz.User},
		{Pattern: "/api/v1/me/sessions", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/me/sessions/revoke-others", Methods: post, Role: authz.User},
		{Pattern: "/api/v1/me/sessions/{id}", Methods: del, Role: authz.User},
		{Pattern: "/api/v1/me/theme", Methods: []string{"PUT"}, Role: authz.User},
		{Pattern: "/api/v1/notifications", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/requests", Methods: []string{"GET", "POST"}, Role: authz.User},
		{Pattern: "/api/v1/requests/search", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/service/catalog", Methods: []string{"PUT"}, Role: authz.Service("catalog:publish")},
		{Pattern: "/api/v1/service/token", Methods: post, Role: authz.Public},
		{Pattern: "/api/v1/themes", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/themes/effective", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/themes/household", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/themes/household/set", Methods: []string{"PUT"}, Role: authz.AdminFresh},
		{Pattern: "/api/v1/widgets/services", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/widgets/storage", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/widgets/system", Methods: get, Role: authz.User},
		{Pattern: "/api/v1/widgets/upcoming", Methods: get, Role: authz.User},
		{Pattern: "/healthz", Methods: get, Role: authz.Public},
	}
	if got := a.Routes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("route table changed:\n got %+v\nwant %+v", got, want)
	}
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Nexus-CSRF", "1")
	}
	h.ServeHTTP(rr, req)
	return rr
}

func TestHealthzAndAPINotFound(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	rr := do(t, a.Handler(), http.MethodGet, "/healthz")
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("healthz: %d %s", rr.Code, rr.Body.String())
	}
	for _, m := range allMethods {
		rr := do(t, a.Handler(), m, "/api/anything/here")
		if rr.Code != 404 {
			t.Errorf("%s /api/anything: %d", m, rr.Code)
			continue
		}
		if m == http.MethodHead {
			continue
		}
		var env struct{ Error struct{ Code string } }
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code != "not_found" {
			t.Errorf("%s: envelope %s", m, rr.Body.String())
		}
	}
	if rr := do(t, a.Handler(), http.MethodPost, "/healthz"); rr.Code != 405 {
		t.Errorf("POST /healthz: %d", rr.Code)
	}
}

func TestCatalogLoadedOrEmpty(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	if a.Catalog().Get().Revision != 1 {
		t.Fatal("example catalog not loaded")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, logs := newApp(t, testConfig(t, bad))
	if b.Catalog().Get().Revision != 0 || !strings.Contains(logs.String(), "empty catalog") {
		t.Fatalf("invalid catalog should start empty; logs %s", logs.String())
	}
}

func TestStartupRefusals(t *testing.T) {
	cfg := testConfig(t, "")
	if err := os.Chmod(cfg.SecretsFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{}); err == nil {
		t.Error("world-readable secrets accepted")
	}

	cfg = testConfig(t, "")
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{}); err == nil {
		t.Error("0755 data dir accepted")
	}
}

func TestSecretsNeverLogged(t *testing.T) {
	cfg := testConfig(t, "")
	a, logs := newApp(t, cfg)
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	resp, err := testutil.LocalClient().Get(srv.URL + "/healthz?x=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	raw := bytes.Repeat([]byte{secretKeyByte}, 32)
	b64 := base64.StdEncoding.EncodeToString(raw)
	if strings.Contains(logs.String(), b64) || bytes.Contains(logs.Bytes(), raw) {
		t.Fatal("secret value found in logs")
	}
}

func TestServeAndGracefulShutdown(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.ServeListener(ctx, ln) }()
	resp, err := testutil.LocalClient().Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestNetworkGuardActive(t *testing.T) {
	if _, err := http.Get("http://example.com/"); err == nil {
		t.Fatal("outbound request succeeded; network guard not active")
	}
}

var testSite = fstest.MapFS{
	"index.html":                    {Data: []byte(`<!doctype html><html><body><div><script>boot()</script></div></body></html>`)},
	"_app/immutable/entry/start.js": {Data: []byte("export{}")},
}

func TestFrontendServedWithStrictCSP(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	for _, p := range []string{"/", "/some/client/route", "/_app/immutable/entry/start.js", "/healthz", "/api/x"} {
		rr := do(t, a.Handler(), http.MethodGet, p)
		csp := rr.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || !strings.Contains(csp, "'sha256-") {
			t.Errorf("%s: CSP %q", p, csp)
		}
		if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") {
			t.Errorf("%s: CSP missing directives: %q", p, csp)
		}
	}
	if rr := do(t, a.Handler(), http.MethodGet, "/some/client/route"); rr.Code != 200 || !strings.Contains(rr.Body.String(), "boot()") {
		t.Fatalf("SPA fallback: %d", rr.Code)
	}
	if rr := do(t, a.Handler(), http.MethodPost, "/"); rr.Code != 405 {
		t.Fatalf("POST /: %d", rr.Code)
	}
}

// The hash Chromium reports for SvelteKit's announcer style attribute.
func TestAnnouncerStyleHash(t *testing.T) {
	const reported = "'sha256-S8qMpvofolR8Mpjy4kQvEm7m1q8clzU4dfDH0AmvZjo='"
	if got := styleAttrSources(); got != "'unsafe-hashes' "+reported {
		t.Fatalf("style-src-attr = %s", got)
	}
}

// ST-2: the complete Content-Security-Policy, exactly. Any change to the
// policy must be made here deliberately.
func TestCSPSnapshot(t *testing.T) {
	a, _ := newApp(t, testConfig(t, ""))
	sum := sha256.Sum256([]byte("boot()"))
	scriptHash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	want := "default-src 'self'; script-src 'self' " + scriptHash + "; style-src 'self'; " +
		"style-src-attr 'unsafe-hashes' 'sha256-S8qMpvofolR8Mpjy4kQvEm7m1q8clzU4dfDH0AmvZjo='; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'none'; " +
		"frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'"
	got := do(t, a.Handler(), http.MethodGet, "/healthz").Header().Get("Content-Security-Policy")
	if got != want {
		t.Fatalf("CSP changed:\n got %s\nwant %s", got, want)
	}
}
