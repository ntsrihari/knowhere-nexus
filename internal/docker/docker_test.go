package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

const id1 = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"

func TestListReadOnlyAndFiltered(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/containers/json":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": id1, "Names": []string{"/jellyfin"}, "Image": "jellyfin/jellyfin:latest", "State": "running"},
				{"Id": "not-an-id", "Names": []string{"/bad"}},
			})
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"RestartCount": 3,
				"Config": map[string]any{"Env": []string{"API_KEY=SECRETVALUE"}}})
		case strings.HasSuffix(r.URL.Path, "/stats"):
			_ = json.NewEncoder(w).Encode(map[string]any{"memory_stats": map[string]any{"usage": 1000, "stats": map[string]any{"inactive_file": 200}}})
		}
	}))
	defer srv.Close()
	list, err := New(upstream.New("docker", srv.URL)).List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "jellyfin" || list[0].RestartCount != 3 || list[0].MemBytes != 800 {
		t.Fatalf("list: %+v", list)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "SECRETVALUE") || strings.Contains(string(b), id1) {
		t.Fatalf("unfiltered data leaked: %s", b)
	}
	for _, s := range seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Fatalf("non-GET request: %s", s)
		}
	}
}

// Contract test (A-03): the package source contains only GET requests to the
// three allowlisted endpoints.
func TestSourceHasNoWriteCalls(t *testing.T) {
	src, err := os.ReadFile("docker.go")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`Method:\s*http\.Method(Post|Put|Patch|Delete)|"(POST|PUT|PATCH|DELETE)"`).Match(src) {
		t.Fatal("write method in docker client")
	}
	for _, m := range regexp.MustCompile(`Path:\s*"([^"]+)"`).FindAllSubmatch(src, -1) {
		if string(m[1]) != "/containers/json" && string(m[1]) != "/containers/" {
			t.Fatalf("unexpected path %s", m[1])
		}
	}
	for _, bad := range []string{"/exec", "/create", "/start", "/stop", "/restart", "/kill", "/volumes", "/images", "/networks", "/build"} {
		if strings.Contains(string(src), `"`+bad) {
			t.Fatalf("forbidden endpoint %s", bad)
		}
	}
	_ = allowedPaths
}
