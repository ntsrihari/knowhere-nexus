package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const indexHTML = `<!doctype html><html><head><script src="/_app/x.js"></script></head>` +
	`<body><div><script>
				{ console.log("boot"); }
			</script></div></body></html>`

func site(t *testing.T) *Site {
	t.Helper()
	s, err := New(fstest.MapFS{
		"index.html":                    {Data: []byte(indexHTML)},
		"favicon.svg":                   {Data: []byte("<svg/>")},
		"_app/immutable/entry/start.js": {Data: []byte("export{}")},
		"_app/immutable/assets/0.css":   {Data: []byte("body{}")},
		"_app/version.json":             {Data: []byte(`{"version":"1"}`)},
		".gitkeep":                      {Data: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = target // bypass client-side normalisation: test raw paths
	h.ServeHTTP(rr, req)
	return rr
}

func TestInlineScriptHashes(t *testing.T) {
	body := "\n\t\t\t\t{ console.log(\"boot\"); }\n\t\t\t"
	sum := sha256.Sum256([]byte(body))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	got := InlineScriptHashes([]byte(indexHTML))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("hashes = %v, want [%s]", got, want)
	}
}

func TestServesFilesWithTypesAndCaching(t *testing.T) {
	s := site(t)
	cases := []struct{ path, ctype, cache string }{
		{"/", "text/html; charset=utf-8", "no-cache"},
		{"/index.html", "text/html; charset=utf-8", "no-cache"},
		{"/favicon.svg", "image/svg+xml", "no-cache"},
		{"/_app/immutable/entry/start.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/_app/immutable/assets/0.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/_app/version.json", "application/json", "no-cache"},
	}
	for _, c := range cases {
		rr := get(t, s, c.path)
		if rr.Code != 200 || rr.Header().Get("Content-Type") != c.ctype || rr.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s: %d %q %q", c.path, rr.Code, rr.Header().Get("Content-Type"), rr.Header().Get("Cache-Control"))
		}
	}
}

func TestSPAFallbackAndMissingAssets(t *testing.T) {
	s := site(t)
	for _, p := range []string{"/settings", "/admin/audit", "/_app", "/_app/immutable"} {
		rr := get(t, s, p)
		if p == "/_app" || p == "/_app/immutable" {
			// Directories are never listed; under _app they are 404.
			if rr.Code != 404 || strings.Contains(rr.Body.String(), "start.js") {
				t.Errorf("%s: %d %s", p, rr.Code, rr.Body.String())
			}
			continue
		}
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), "<!doctype html>") {
			t.Errorf("%s: SPA fallback missing: %d", p, rr.Code)
		}
	}
	for _, p := range []string{"/_app/immutable/missing.js", "/missing.css", "/robots.txt"} {
		if rr := get(t, s, p); rr.Code != 404 {
			t.Errorf("%s: %d, want 404", p, rr.Code)
		}
	}
}

// ST-7: traversal attempts never escape the embedded filesystem.
func TestTraversal(t *testing.T) {
	s := site(t)
	attacks := []string{
		"/../../etc/passwd", "/..%2f..%2fetc/passwd", "/%2e%2e/%2e%2e/etc/passwd",
		"/%252e%252e/etc/passwd", "/..\\..\\etc\\passwd", "/_app/../../../etc/passwd",
		"//etc/passwd", "/etc/passwd", "/.gitkeep", "/_app/immutable/../../../../go.mod",
		"/index.html\x00.js",
	}
	for _, a := range attacks {
		rr := get(t, s, a)
		body := rr.Body.String()
		if strings.Contains(body, "root:") || strings.Contains(body, "module ") {
			t.Errorf("%s: leaked host file", a)
		}
		if rr.Code == 200 && !strings.Contains(body, "<!doctype html>") {
			t.Errorf("%s: served unexpected content: %q", a, body)
		}
	}
	// Through the real HTTP stack as well (client-side normalisation off).
	srv := httptest.NewServer(s)
	defer srv.Close()
	for _, a := range []string{"/..%2f..%2fetc/passwd", "/%2e%2e/%2e%2e/etc/passwd"} {
		resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Get(srv.URL + a)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 && resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s via HTTP: unexpected %s", a, resp.Header.Get("Content-Type"))
		}
	}
}

func TestUnbuiltFrontend(t *testing.T) {
	s, err := New(fstest.MapFS{".gitkeep": {}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Built() {
		t.Fatal("empty build reported as built")
	}
	if rr := get(t, s, "/"); rr.Code != 503 {
		t.Fatalf("unbuilt /: %d", rr.Code)
	}
}

func TestEmbeddedIsReadable(t *testing.T) {
	if _, err := New(Embedded()); err != nil {
		t.Fatal(err)
	}
}
