package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/config"
)

type fakeServices struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	apiUser  string
	keys     []string
}

func newFakeServices(t *testing.T) *fakeServices {
	f := &fakeServices{}
	mux := http.NewServeMux()
	key := func(w http.ResponseWriter, r *http.Request, want string) bool {
		f.mu.Lock()
		f.keys = append(f.keys, r.Header.Get("X-Api-Key"))
		f.mu.Unlock()
		if r.Header.Get("X-Api-Key") != want {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /immich/api/server/statistics", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "imm-key") {
			_, _ = w.Write([]byte(`{"photos":10,"videos":2,"usage":12345}`))
		}
	})
	mux.HandleFunc("POST /pihole/api/auth", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"session":{"valid":true,"sid":"S1"}}`))
	})
	mux.HandleFunc("GET /pihole/api/stats/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-FTL-SID") != "S1" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"queries":{"total":1000,"blocked":123,"percent_blocked":12.34}}`))
	})
	mux.HandleFunc("POST /qbit/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("username") != "qu" || r.PostForm.Get("password") != "qp" {
			_, _ = w.Write([]byte("Fails."))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "Q1"})
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("GET /qbit/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("SID"); c == nil || c.Value != "Q1" {
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`[{"hash":"h1","name":"Some.Film","progress":0.5,"state":"downloading","dlspeed":100,"upspeed":1,"eta":60,"category":"movies"}]`))
	})
	for _, svc := range []string{"radarr", "sonarr"} {
		svc := svc
		mux.HandleFunc("GET /"+svc+"/api/v3/queue", func(w http.ResponseWriter, r *http.Request) {
			if key(w, r, svc+"-key") {
				_, _ = w.Write([]byte(`{"records":[{"title":"` + svc + ` item","status":"downloading","size":100,"sizeleft":25,"timeleft":"00:10:00"}]}`))
			}
		})
	}
	mux.HandleFunc("GET /radarr/api/v3/calendar", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "radarr-key") {
			_, _ = w.Write([]byte(`[{"title":"New Film","digitalRelease":"2026-11-20T00:00:00Z"}]`))
		}
	})
	mux.HandleFunc("GET /sonarr/api/v3/calendar", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "sonarr-key") {
			_, _ = w.Write([]byte(`[{"title":"Pilot","airDateUtc":"2026-11-16T20:00:00Z","seasonNumber":1,"episodeNumber":1,"series":{"title":"Show"}}]`))
		}
	})
	mux.HandleFunc("GET /prowlarr/api/v1/indexer", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "prow-key") {
			_, _ = w.Write([]byte(`[{"name":"A","enable":true},{"name":"B","enable":false}]`))
		}
	})
	mux.HandleFunc("GET /prowlarr/api/v1/indexerstatus", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("GET /seerr/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "seerr-key") {
			_, _ = w.Write([]byte(`{"results":[{"id":7,"jellyfinUserId":"user1"}]}`))
		}
	})
	mux.HandleFunc("GET /seerr/api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "seerr-key") {
			_, _ = w.Write([]byte(`{"results":[{"id":603,"mediaType":"movie","title":"The Matrix","releaseDate":"1999-03-31"},{"id":1,"mediaType":"person","name":"X"}]}`))
		}
	})
	mux.HandleFunc("POST /seerr/api/v1/request", func(w http.ResponseWriter, r *http.Request) {
		if !key(w, r, "seerr-key") {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, body)
		f.apiUser = r.Header.Get("X-Api-User")
		f.mu.Unlock()
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	mux.HandleFunc("GET /seerr/api/v1/request", func(w http.ResponseWriter, r *http.Request) {
		if key(w, r, "seerr-key") {
			_, _ = w.Write([]byte(`{"results":[{"id":1,"status":2,"type":"movie","media":{"tmdbId":603,"status":3},"requestedBy":{"displayName":"bob"}}]}`))
		}
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func newIntegrationEnv(t *testing.T) (*env, *fakeServices) {
	fs := newFakeServices(t)
	e := newEnvWith(t, func(c *config.Config) {
		c.Integrations = map[string]string{"immich": fs.URL + "/immich", "pihole": fs.URL + "/pihole", "qbittorrent": fs.URL + "/qbit",
			"radarr": fs.URL + "/radarr", "sonarr": fs.URL + "/sonarr", "prowlarr": fs.URL + "/prowlarr", "jellyseerr": fs.URL + "/seerr"}
	}, "jellyfinAdminKey: JF-ADMIN-KEY\nintegrations:\n  immich: imm-key\n  pihole: pi-pass\n  qbittorrent: qu:qp\n"+
		"  radarr: radarr-key\n  sonarr: sonarr-key\n  prowlarr: prow-key\n  jellyseerr: seerr-key\n")
	e.jf.AdminKey = "JF-ADMIN-KEY"
	e.jf.Sessions = []map[string]any{{"Id": "s1", "UserName": "bob", "Client": "Milano", "DeviceName": "TV",
		"NowPlayingItem":  map[string]any{"Name": "The Matrix", "Type": "Movie", "RunTimeTicks": 1000},
		"PlayState":       map[string]any{"PositionTicks": 250, "PlayMethod": "Transcode"},
		"TranscodingInfo": map[string]any{"TranscodeReasons": []string{"VideoCodecNotSupported"}}}}
	e.jf.Tasks = []map[string]any{{"Id": "t1", "Name": "Scan Media Library", "Category": "Library", "State": "Idle"}}
	for _, w := range e.app.widgets.list {
		_ = w.refresh(context.Background(), e.app.now)
	}
	return e, fs
}

func TestIntegrationWidgets(t *testing.T) {
	e, fs := newIntegrationEnv(t)
	alice, bob := e.client(), e.client()
	alice.login("alice", "apw")
	bob.login("bob", "bpw")
	checks := map[string]string{
		"/api/v1/admin/widgets/now-playing": `"playMethod":"transcode"`,
		"/api/v1/admin/widgets/photos":      `"assets":12`,
		"/api/v1/admin/widgets/dns":         `"blockedPercent":12.3`,
		"/api/v1/admin/widgets/downloads":   `"progress":50`,
		"/api/v1/admin/widgets/indexers":    `"enabled":1`,
		"/api/v1/admin/arr/queue":           `"progress":75`,
	}
	for path, want := range checks {
		code, b := alice.do("GET", path, nil)
		if code != 200 || !strings.Contains(string(b), want) || !strings.Contains(string(b), `"status":"ok"`) {
			t.Errorf("%s: %d %s", path, code, b)
		}
		if code, _ := bob.do("GET", path, nil); code != 403 {
			t.Errorf("%s as user: %d", path, code)
		}
	}
	if _, b := bob.do("GET", "/api/v1/widgets/upcoming", nil); !strings.Contains(string(b), "New Film") || !strings.Contains(string(b), "Show · S01E01") {
		t.Errorf("upcoming: %s", b)
	}
	// Keys never reach clients.
	for _, p := range []string{"/api/v1/admin/status", "/api/v1/admin/widgets/photos"} {
		_, b := alice.do("GET", p, nil)
		for _, k := range []string{"imm-key", "JF-ADMIN-KEY", "seerr-key", "pi-pass", "qp"} {
			if strings.Contains(string(b), k) {
				t.Fatalf("%s leaked %s", p, k)
			}
		}
	}
	_ = fs
}

func TestRequestsAsUser(t *testing.T) {
	e, fs := newIntegrationEnv(t)
	bob := e.client()
	bob.login("bob", "bpw")
	code, b := bob.do("GET", "/api/v1/requests/search?q=matrix", nil)
	if code != 200 || !strings.Contains(string(b), `"tmdbId":603`) || strings.Contains(string(b), "person") {
		t.Fatalf("search: %d %s", code, b)
	}
	if code, _ := bob.do("POST", "/api/v1/requests", map[string]any{"mediaType": "movie", "tmdbId": 603}); code != 201 {
		t.Fatalf("request: %d", code)
	}
	if fs.apiUser != "7" || len(fs.requests) != 1 {
		t.Fatalf("not attributed to the user: %q %v", fs.apiUser, fs.requests)
	}
	if code, b := bob.do("GET", "/api/v1/requests", nil); code != 200 || !strings.Contains(string(b), `"status":"approved"`) {
		t.Fatalf("list: %d %s", code, b)
	}
	alice := e.client()
	alice.login("alice", "apw")
	if code, _ := alice.do("POST", "/api/v1/requests", map[string]any{"mediaType": "movie", "tmdbId": 603}); code != 409 {
		t.Fatalf("unlinked user: %d", code)
	}
}

func TestJellyfinTasksAndAnalytics(t *testing.T) {
	e, _ := newIntegrationEnv(t)
	alice := e.client()
	alice.login("alice", "apw")
	if code, b := alice.do("GET", "/api/v1/admin/jellyfin/tasks", nil); code != 200 || !strings.Contains(string(b), "Scan Media Library") {
		t.Fatalf("tasks: %d %s", code, b)
	}
	if code, _ := alice.do("POST", "/api/v1/admin/jellyfin/tasks/nope/run", nil); code != 404 {
		t.Fatalf("unknown task: %d", code)
	}
	if code, _ := alice.do("POST", "/api/v1/admin/jellyfin/tasks/t1/run", nil); code != 202 || len(e.jf.TaskRuns) != 1 {
		t.Fatalf("run task: %d %v", code, e.jf.TaskRuns)
	}
	_, b := alice.do("GET", "/api/v1/admin/analytics?days=7", nil)
	if !strings.Contains(string(b), `"user":"bob"`) || !strings.Contains(string(b), `"transcodePercent":100`) {
		t.Fatalf("analytics: %s", b)
	}
	// Stream start produced an admin-only notification.
	if _, b := alice.do("GET", "/api/v1/notifications", nil); !strings.Contains(string(b), "stream.started") {
		t.Fatalf("admin notifications: %s", b)
	}
	bob := e.client()
	bob.login("bob", "bpw")
	if _, b := bob.do("GET", "/api/v1/notifications", nil); strings.Contains(string(b), "stream.started") {
		t.Fatalf("user saw admin notification: %s", b)
	}
}
