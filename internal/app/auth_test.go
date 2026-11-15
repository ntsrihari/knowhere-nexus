package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/testutil"
)

type env struct {
	t   *testing.T
	ts  *httptest.Server
	app *App
	jf  *testutil.FakeJellyfin
}

type client struct {
	e   *env
	h   *http.Client
	tok string // bearer, if set
}

func newEnv(t *testing.T) *env {
	t.Helper()
	jf := testutil.NewFakeJellyfin(
		testutil.FakeUser{ID: "admin1", Name: "alice", Password: "apw", Admin: true},
		testutil.FakeUser{ID: "user1", Name: "bob", Password: "bpw"},
	)
	t.Cleanup(jf.Close)
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	cfg := testConfig(t, "")
	cfg.JellyfinURL = jf.URL
	cfg.PublicOrigin = ts.URL
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Static: testSite})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	ts.Config.Handler = a.Handler()
	return &env{t: t, ts: ts, app: a, jf: jf}
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, h: &http.Client{Transport: e.ts.Client().Transport, Jar: jar}}
}

func (c *client) do(method, path string, body any, hdr ...string) (int, []byte) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Nexus-CSRF", "1")
		req.Header.Set("Origin", c.e.ts.URL)
	}
	if c.tok != "" {
		req.Header.Set("Authorization", "Bearer "+c.tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := c.h.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c *client) login(user, pw string) int {
	code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pw})
	return code
}

func TestLoginSessionLogout(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if code, _ := c.do("GET", "/api/v1/auth/session", nil); code != 401 {
		t.Fatalf("anonymous session: %d", code)
	}
	if code := c.login("alice", "apw"); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if e.jf.ActiveTokens() != 0 {
		t.Fatal("Jellyfin token retained (Q-01)")
	}
	code, b := c.do("GET", "/api/v1/auth/session", nil)
	if code != 200 || !strings.Contains(string(b), `"role":"admin"`) {
		t.Fatalf("session: %d %s", code, b)
	}
	if code, _ := c.do("GET", "/api/v1/admin/audit", nil); code != 200 {
		t.Fatalf("admin audit as admin: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/auth/logout", nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/auth/session", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

func TestCookieAttributes(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"bob","password":"bpw"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nexus-CSRF", "1")
	req.Header.Set("Origin", e.ts.URL)
	resp, err := c.h.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var access, refresh *http.Cookie
	for _, ck := range resp.Cookies() {
		switch ck.Name {
		case accessCookie:
			access = ck
		case refreshCookie:
			refresh = ck
		}
	}
	if access == nil || refresh == nil {
		t.Fatal("cookies missing")
	}
	for _, ck := range []*http.Cookie{access, refresh} {
		if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Domain != "" {
			t.Errorf("%s attributes: %+v", ck.Name, ck)
		}
	}
	if access.Path != "/" || access.MaxAge != 900 || refresh.Path != "/api/v1/auth" {
		t.Errorf("paths/ages: %s %d %s", access.Path, access.MaxAge, refresh.Path)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	body := map[string]string{"username": "bob", "password": "bpw"}
	cases := []struct {
		name string
		hdr  []string
		want int
	}{
		{"no csrf header", []string{"X-Nexus-CSRF", ""}, 403},
		{"foreign origin", []string{"Origin", "https://evil.example"}, 403},
		{"sibling subdomain", []string{"Origin", "https://jellyfin.knowhere.example"}, 403},
		{"cross-site fetch without origin", []string{"Origin", "", "Sec-Fetch-Site", "cross-site"}, 403},
		{"native client: header, no origin", []string{"Origin", ""}, 200},
	}
	for _, tc := range cases {
		if code, _ := c.do("POST", "/api/v1/auth/login", body, tc.hdr...); code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, code, tc.want)
		}
	}
	// With session cookies present, a request without origin information is refused.
	if code, _ := c.do("POST", "/api/v1/auth/logout", nil, "Origin", ""); code != 403 {
		t.Errorf("cookie request without origin: %d", code)
	}
}

func TestRolesAndAdminWindow(t *testing.T) {
	e := newEnv(t)
	bob := e.client()
	bob.login("bob", "bpw")
	for _, p := range []string{"/api/v1/admin/audit", "/api/v1/admin/sessions"} {
		if code, _ := bob.do("GET", p, nil); code != 403 {
			t.Errorf("user %s: %d", p, code)
		}
	}
	alice := e.client()
	alice.login("alice", "apw")
	// The 12-hour admin window (Q-02): an admin session without re-auth becomes a user.
	e.app.now = func() time.Time { return time.Now().Add(12*time.Hour + time.Minute) }
	e.app.sessions.Now = func() time.Time { return time.Now().Add(10 * time.Minute) } // keep access token valid
	if code, _ := alice.do("GET", "/api/v1/admin/audit", nil); code != 403 {
		t.Fatalf("admin after 12h without reauth: %d", code)
	}
	e.app.now = time.Now
	// Denied admin attempts are audited.
	list, _ := e.app.audit.List(context.Background(), 0, 50, "", "authz.denied")
	if len(list) == 0 {
		t.Fatal("denied admin attempt not audited")
	}
}

func TestRevokeUserNeedsReauth(t *testing.T) {
	e := newEnv(t)
	bob := e.client()
	bob.login("bob", "bpw")
	alice := e.client()
	alice.login("alice", "apw")
	code, b := alice.do("POST", "/api/v1/admin/users/user1/revoke-sessions", nil)
	if code != 403 || !strings.Contains(string(b), "reauth_required") {
		t.Fatalf("without reauth: %d %s", code, b)
	}
	if code, _ := alice.do("POST", "/api/v1/auth/reauth", map[string]string{"password": "wrong"}); code != 401 {
		t.Fatalf("bad reauth: %d", code)
	}
	if code, _ := alice.do("POST", "/api/v1/auth/reauth", map[string]string{"password": "apw"}); code != 204 {
		t.Fatalf("reauth: %d", code)
	}
	if code, b := alice.do("POST", "/api/v1/admin/users/user1/revoke-sessions", nil); code != 200 || !strings.Contains(string(b), `"revoked":1`) {
		t.Fatalf("revoke user: %d %s", code, b)
	}
	if code, _ := bob.do("GET", "/api/v1/auth/session", nil); code != 401 {
		t.Fatalf("bob still signed in: %d", code)
	}
	list, _ := e.app.audit.List(context.Background(), 0, 50, "", "session.admin_revoke_user")
	if len(list) != 1 || list[0].ActorName != "alice" {
		t.Fatalf("audit: %+v", list)
	}
}

func TestOwnSessionsAndRevokeOthers(t *testing.T) {
	e := newEnv(t)
	a1, a2 := e.client(), e.client()
	a1.login("bob", "bpw")
	a2.login("bob", "bpw")
	code, b := a1.do("GET", "/api/v1/me/sessions", nil)
	var list []map[string]any
	_ = json.Unmarshal(b, &list)
	if code != 200 || len(list) != 2 {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, _ := a1.do("POST", "/api/v1/me/sessions/revoke-others", nil); code != 204 {
		t.Fatalf("revoke others: %d", code)
	}
	if code, _ := a2.do("GET", "/api/v1/auth/session", nil); code != 401 {
		t.Fatal("other session survived")
	}
	if code, _ := a1.do("GET", "/api/v1/auth/session", nil); code != 200 {
		t.Fatal("current session revoked")
	}
	// Another user's session id is not found.
	alice := e.client()
	alice.login("alice", "apw")
	_, b = alice.do("GET", "/api/v1/me/sessions", nil)
	var al []map[string]any
	_ = json.Unmarshal(b, &al)
	if code, _ := a1.do("DELETE", "/api/v1/me/sessions/"+al[0]["id"].(string), nil); code != 404 {
		t.Fatalf("revoked another user's session: %d", code)
	}
}

func TestRefreshCookieFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("bob", "bpw")
	if code, _ := c.do("POST", "/api/v1/auth/refresh", nil); code != 200 {
		t.Fatalf("refresh: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/auth/session", nil); code != 200 {
		t.Fatal("session after refresh")
	}
	anon := e.client()
	if code, _ := anon.do("POST", "/api/v1/auth/refresh", nil); code != 401 {
		t.Fatalf("refresh without cookie: %d", code)
	}
}

func TestTokenExchangeAndBearer(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	jtok := e.jf.IssueToken("bob")
	code, b := c.do("POST", "/api/v1/auth/token-exchange", map[string]string{"jellyfinToken": jtok, "deviceLabel": "Pixel"}, "Origin", "")
	var res struct{ AccessToken, RefreshToken string }
	_ = json.Unmarshal(b, &res)
	if code != 200 || res.AccessToken == "" || res.RefreshToken == "" {
		t.Fatalf("exchange: %d %s", code, b)
	}
	m := e.client()
	m.tok = res.AccessToken
	if code, _ := m.do("GET", "/api/v1/auth/session", nil); code != 200 {
		t.Fatalf("bearer: %d", code)
	}
	// Bearer requests are not subject to CSRF origin checks.
	if code, _ := m.do("POST", "/api/v1/me/sessions/revoke-others", nil, "Origin", "https://elsewhere", "X-Nexus-CSRF", ""); code != 204 {
		t.Fatalf("bearer unsafe method: %d", code)
	}
	code, b = c.do("POST", "/api/v1/auth/refresh", map[string]string{"refreshToken": res.RefreshToken}, "Origin", "")
	if code != 200 || !strings.Contains(string(b), "accessToken") {
		t.Fatalf("body refresh: %d %s", code, b)
	}
	if code, _ := c.do("POST", "/api/v1/auth/token-exchange", map[string]string{"jellyfinToken": "bogus"}, "Origin", ""); code != 401 {
		t.Fatalf("bogus exchange: %d", code)
	}
}

func TestQuickConnectFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	code, b := c.do("POST", "/api/v1/auth/quickconnect", nil)
	var start struct{ Code, PollID string }
	_ = json.Unmarshal(b, &start)
	if code != 200 || start.Code == "" || strings.Contains(string(b), "secret") {
		t.Fatalf("start: %d %s", code, b)
	}
	if _, b := c.do("POST", "/api/v1/auth/quickconnect/"+start.PollID, nil); !strings.Contains(string(b), "pending") {
		t.Fatalf("pending: %s", b)
	}
	e.jf.ApproveQuickConnect(start.Code, "bob")
	if _, b := c.do("POST", "/api/v1/auth/quickconnect/"+start.PollID, nil); !strings.Contains(string(b), "authorized") {
		t.Fatalf("authorized: %s", b)
	}
	if code, _ := c.do("GET", "/api/v1/auth/session", nil); code != 200 {
		t.Fatal("no session after quick connect")
	}
	if _, b := c.do("POST", "/api/v1/auth/quickconnect/"+start.PollID, nil); !strings.Contains(string(b), "expired") {
		t.Fatalf("poll id reused: %s", b)
	}
}

func TestLoginRateLimitAndUpstreamDown(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for i := 0; i < 5; i++ {
		if code := c.login("bob", "nope"); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := c.login("bob", "bpw"); code != 429 {
		t.Fatalf("not rate limited: %d", code)
	}
	if code := c.login("alice", "apw"); code != 200 {
		t.Fatalf("other user blocked: %d", code)
	}
	e.jf.Close()
	if code := e.client().login("alice", "apw"); code != 502 {
		t.Fatalf("Jellyfin down: %d", code)
	}
	// Existing sessions keep working while Jellyfin is down (A-17).
	if code, _ := c.do("GET", "/api/v1/auth/session", nil); code != 200 {
		t.Fatalf("existing session during outage: %d", code)
	}
}

func TestEventsStreamClosedOnRevoke(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("bob", "bpw")
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/events", nil)
	resp, err := c.h.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	for i := 0; i < 50 && e.app.events.count() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := c.do("POST", "/api/v1/auth/logout", nil); code != 204 {
		t.Fatal("logout")
	}
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			sb.WriteString(sc.Text() + "\n")
		}
		done <- sb.String()
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, "session.revoked") {
			t.Fatalf("stream ended without revocation event: %q", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed after revocation")
	}
}

func TestUntrustedPeerRejected(t *testing.T) {
	e := newEnv(t)
	e.app.cfg.TrustedProxies = nil
	a, err := New(context.Background(), e.app.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Static: testSite})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
	req.RemoteAddr = "10.1.2.3:4"
	a.Handler().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("untrusted peer: %d", rr.Code)
	}
}
