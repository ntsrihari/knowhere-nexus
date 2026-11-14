package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: did not panic", name)
		}
	}()
	f()
}

func TestHandleRejectsInvalidEntries(t *testing.T) {
	get := []string{http.MethodGet}
	mustPanic(t, "empty role", func() { NewRegistry().Handle("/a", "", get, ok) })
	mustPanic(t, "unknown role", func() { NewRegistry().Handle("/a", "superuser", get, ok) })
	mustPanic(t, "bad service scope", func() { NewRegistry().Handle("/a", Service("x y"), get, ok) })
	mustPanic(t, "no methods", func() { NewRegistry().Handle("/a", Public, nil, ok) })
	mustPanic(t, "bad method", func() { NewRegistry().Handle("/a", Public, []string{"TRACE"}, ok) })
	mustPanic(t, "nil handler", func() { NewRegistry().Handle("/a", Public, get, nil) })
	mustPanic(t, "method in pattern", func() { NewRegistry().Handle("GET /a", Public, get, ok) })
	mustPanic(t, "duplicate", func() {
		r := NewRegistry()
		r.Handle("/a", Public, get, ok)
		r.Handle("/a", Public, get, ok)
	})
}

func TestBuildRefusesInvalidTable(t *testing.T) {
	// Entries injected around Handle simulate a corrupted table: Build must
	// still refuse to produce a mux (the server then exits non-zero).
	reg := NewRegistry()
	reg.routes = append(reg.routes, Route{Pattern: "/a", Methods: []string{"GET"}, Handler: ok})
	if _, err := reg.Build(PublicOnly{}); err == nil {
		t.Fatal("route without role accepted")
	}
	if _, err := NewRegistry().Build(PublicOnly{}); err == nil {
		t.Fatal("empty table accepted")
	}
	conflict := NewRegistry()
	conflict.routes = append(conflict.routes,
		Route{Pattern: "/{x}/b", Methods: []string{"GET"}, Role: Public, Handler: ok},
		Route{Pattern: "/a/{y}", Methods: []string{"GET"}, Role: Public, Handler: ok})
	if _, err := conflict.Build(PublicOnly{}); err == nil {
		t.Fatal("conflicting patterns accepted")
	}
}

func TestPublicOnlyGuardDeniesEveryOtherRole(t *testing.T) {
	roles := []Role{Public, User, Admin, AdminFresh, AdminFreshReauth, Service("catalog:publish")}
	reg := NewRegistry()
	paths := map[Role]string{}
	for i, r := range roles {
		p := "/r" + string(rune('a'+i))
		paths[r] = p
		reg.Handle(p, r, []string{http.MethodGet}, ok)
	}
	h, err := reg.Build(PublicOnly{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, paths[r], nil))
		want := http.StatusUnauthorized
		if r == Public {
			want = http.StatusTeapot
		}
		if rr.Code != want {
			t.Errorf("role %s: status %d, want %d", r, rr.Code, want)
		}
	}
}

func TestMethodEnforcement(t *testing.T) {
	reg := NewRegistry()
	reg.Handle("/a", Public, []string{http.MethodGet}, ok)
	h, err := reg.Build(PublicOnly{})
	if err != nil {
		t.Fatal(err)
	}
	for m, want := range map[string]int{"GET": 418, "HEAD": 418, "POST": 405, "DELETE": 405, "OPTIONS": 405} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(m, "/a", nil))
		if rr.Code != want {
			t.Errorf("%s: %d, want %d", m, rr.Code, want)
		}
		if want == 405 && rr.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: Allow = %q", m, rr.Header().Get("Allow"))
		}
	}
}
