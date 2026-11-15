package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/config"
)

func fakeDocker(t *testing.T) *httptest.Server {
	t.Helper()
	const jf = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	const pt = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": jf, "Names": []string{"/jellyfin"}, "Image": "jellyfin/jellyfin", "State": "running"},
				{"Id": pt, "Names": []string{"/portainer"}, "Image": "portainer", "State": "exited"},
			})
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"RestartCount":2}`))
		default:
			_, _ = w.Write([]byte(`{"memory_stats":{"usage":100}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWidgetsViewsAndStates(t *testing.T) {
	e := newEnv(t)
	e.app.cfg.DockerURL = fakeDocker(t).URL
	e.app.cfg.Mounts = []config.Mount{{Label: "Root", Path: "/", HostPath: "/"}, {Label: "Gone", Path: "/definitely/not/mounted", HostPath: "/x"}}
	e.app.widgets = newWidgetSet(e.app)
	ctx := context.Background()
	for _, w := range e.app.widgets.list {
		if err := w.refresh(ctx, e.app.now); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
	}
	bob, alice := e.client(), e.client()
	bob.login("bob", "bpw")
	alice.login("alice", "apw")

	code, b := bob.do("GET", "/api/v1/widgets/system", nil)
	if code != 200 || !strings.Contains(string(b), `"status":"ok"`) || !strings.Contains(string(b), "memTotal") {
		t.Fatalf("system: %d %s", code, b)
	}
	_, b = bob.do("GET", "/api/v1/widgets/storage", nil)
	if strings.Contains(string(b), `"path"`) || !strings.Contains(string(b), `"label":"Gone","health":"unavailable"`) {
		t.Fatalf("user storage leaks admin detail or misses unavailable: %s", b)
	}
	_, b = alice.do("GET", "/api/v1/admin/widgets/storage", nil)
	if !strings.Contains(string(b), `"excluded":true`) || !strings.Contains(string(b), "/mnt/internal-hdd") {
		t.Fatalf("admin storage: %s", b)
	}
	_, b = bob.do("GET", "/api/v1/widgets/services", nil)
	if strings.Contains(string(b), "portainer") || strings.Contains(string(b), "restartCount") || !strings.Contains(string(b), `"catalogId":"jellyfin","name":"Jellyfin","up":true`) {
		t.Fatalf("user services: %s", b)
	}
	_, b = alice.do("GET", "/api/v1/admin/widgets/services", nil)
	if !strings.Contains(string(b), `"restartCount":2`) || !strings.Contains(string(b), "portainer") {
		t.Fatalf("admin services: %s", b)
	}
	if code, _ := bob.do("GET", "/api/v1/admin/widgets/services", nil); code != 403 {
		t.Fatalf("user admin widget: %d", code)
	}
}

func TestWidgetNotConfiguredAndUnavailable(t *testing.T) {
	e := newEnv(t)
	bob := e.client()
	bob.login("bob", "bpw")
	if _, b := bob.do("GET", "/api/v1/widgets/services", nil); !strings.Contains(string(b), stNotConfigured) {
		t.Fatalf("docker not configured: %s", b)
	}
	if _, b := bob.do("GET", "/api/v1/widgets/system", nil); !strings.Contains(string(b), stUnavailable) {
		t.Fatalf("never polled: %s", b)
	}
	// A failing downstream leaves other widgets working.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	dead.Close()
	e.app.cfg.DockerURL = dead.URL
	e.app.widgets = newWidgetSet(e.app)
	if err := e.app.widgets.byName["services"].refresh(context.Background(), e.app.now); err == nil {
		t.Fatal("dead docker should fail")
	}
	if err := e.app.widgets.byName["system"].refresh(context.Background(), e.app.now); err != nil {
		t.Fatal(err)
	}
	if _, b := bob.do("GET", "/api/v1/widgets/services", nil); !strings.Contains(string(b), stUnavailable) {
		t.Fatalf("dead docker: %s", b)
	}
	if _, b := bob.do("GET", "/api/v1/widgets/system", nil); !strings.Contains(string(b), `"status":"ok"`) {
		t.Fatalf("system affected by docker failure: %s", b)
	}
}

func TestLauncherAppsAndCatalogReload(t *testing.T) {
	e := newEnv(t)
	bob, alice := e.client(), e.client()
	bob.login("bob", "bpw")
	alice.login("alice", "apw")
	_, b := bob.do("GET", "/api/v1/apps", nil)
	if strings.Contains(string(b), "portainer") || !strings.Contains(string(b), `"id":"jellyfin"`) || !strings.Contains(string(b), `"available":false`) {
		t.Fatalf("user apps: %s", b)
	}
	_, b = alice.do("GET", "/api/v1/apps", nil)
	if !strings.Contains(string(b), "portainer") {
		t.Fatalf("admin apps: %s", b)
	}
	// Reload: same revision and content is a no-op; a bad file keeps the old catalog.
	if code, _ := alice.do("POST", "/api/v1/admin/catalog/reload", nil); code != 200 {
		t.Fatalf("reload: %d", code)
	}
	bad := filepath.Join(t.TempDir(), "c.yaml")
	_ = os.WriteFile(bad, []byte("schema: nope\n"), 0o600)
	e.app.cfg.CatalogPath = bad
	if code, _ := alice.do("POST", "/api/v1/admin/catalog/reload", nil); code != 422 {
		t.Fatalf("bad reload: %d", code)
	}
	if e.app.catalog.Get().Revision != 1 {
		t.Fatal("catalog replaced by invalid file")
	}
	if code, _ := bob.do("POST", "/api/v1/admin/catalog/reload", nil); code != 403 {
		t.Fatalf("user reload: %d", code)
	}
}

func TestThemePrecedenceAndLayout(t *testing.T) {
	e := newEnv(t)
	bob, alice := e.client(), e.client()
	bob.login("bob", "bpw")
	alice.login("alice", "apw")
	_, b := bob.do("GET", "/api/v1/themes/effective", nil)
	if !strings.Contains(string(b), `"from":"builtin"`) {
		t.Fatalf("builtin: %s", b)
	}
	house := `{"schema":"knowhere.theme","schemaVersion":"0.1.0","seedColor":"#336699","colorScheme":"light","cornerRadius":8,"font":"roboto","density":"compact"}`
	if code, _ := bob.do("PUT", "/api/v1/themes/household/set", json.RawMessage(house)); code != 403 {
		t.Fatalf("user set household: %d", code)
	}
	if code, b := alice.do("PUT", "/api/v1/themes/household/set", json.RawMessage(house)); code != 200 {
		t.Fatalf("set household: %d %s", code, b)
	}
	if _, b := bob.do("GET", "/api/v1/themes/effective", nil); !strings.Contains(string(b), `"from":"household"`) || !strings.Contains(string(b), "#336699") {
		t.Fatalf("household: %s", b)
	}
	mine := strings.Replace(house, "#336699", "#aa0000", 1)
	if code, _ := bob.do("PUT", "/api/v1/me/theme", json.RawMessage(mine)); code != 200 {
		t.Fatal("set mine")
	}
	if _, b := bob.do("GET", "/api/v1/themes/effective", nil); !strings.Contains(string(b), `"from":"user"`) {
		t.Fatalf("user theme: %s", b)
	}
	if code, b := bob.do("PUT", "/api/v1/me/theme", json.RawMessage(strings.Replace(house, "0.1.0", "1.0.0", 1))); code != 422 || !strings.Contains(string(b), "unsupported_schema_version") {
		t.Fatalf("bad version: %d %s", code, b)
	}
	if code, _ := bob.do("PUT", "/api/v1/me/home-layout", map[string]any{"tiles": []string{"jellyfin"}, "widgets": []string{"system"}}); code != 200 {
		t.Fatal("layout")
	}
	if code, _ := bob.do("PUT", "/api/v1/me/home-layout", map[string]any{"tiles": []string{"nope"}}); code != 422 {
		t.Fatal("unknown tile accepted")
	}
	if _, b := bob.do("GET", "/api/v1/me", nil); !strings.Contains(string(b), "#aa0000") || !strings.Contains(string(b), `"jellyfin"`) {
		t.Fatalf("me: %s", b)
	}
}
