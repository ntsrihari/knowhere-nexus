package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/config"
)

type svcEnv struct {
	*env
	priv ed25519.PrivateKey
}

func newServiceEnv(t *testing.T) *svcEnv {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	e := newEnvWith(t, func(c *config.Config) {
		c.Services = []config.Service{{ID: "collector", PublicKey: pub, Scopes: []string{"catalog:publish"}}}
		c.Routes = []config.Route{{ID: "store", URL: "/store/", AuthMode: "nexus", Audience: "collector", ApprovedBy: "owner", ApprovedOn: "2026-11-15"}}
	}, "signingKey: "+base64.StdEncoding.EncodeToString(seed)+"\n")
	return &svcEnv{env: e, priv: priv}
}

func (s *svcEnv) assertion(t *testing.T, mutate func(*claims)) string {
	now := time.Now()
	c := claims{Iss: "collector", Sub: "collector", Aud: s.app.tokenURL(), Iat: now.Unix(), Exp: now.Add(50 * time.Second).Unix(), Jti: randomID()}
	if mutate != nil {
		mutate(&c)
	}
	return signJWT(s.priv, "c1", c)
}

func (s *svcEnv) token(t *testing.T, assertion, scope string) (int, string) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion": {assertion}, "scope": {scope}}
	resp, err := s.ts.Client().PostForm(s.ts.URL+"/api/v1/service/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out.AccessToken
}

func TestServiceTokenIssuance(t *testing.T) {
	s := newServiceEnv(t)
	a := s.assertion(t, nil)
	code, tok := s.token(t, a, "catalog:publish")
	if code != 200 || tok == "" {
		t.Fatalf("issue: %d", code)
	}
	if code, _ := s.token(t, a, "catalog:publish"); code != 401 {
		t.Fatalf("replayed assertion: %d", code)
	}
	for name, m := range map[string]func(*claims){
		"wrong audience": func(c *claims) { c.Aud = "https://evil" },
		"expired":        func(c *claims) { c.Exp = time.Now().Add(-time.Hour).Unix() },
		"too long":       func(c *claims) { c.Exp = time.Now().Add(time.Hour).Unix() },
		"other issuer":   func(c *claims) { c.Iss, c.Sub = "milano", "milano" },
	} {
		if code, _ := s.token(t, s.assertion(t, m), "catalog:publish"); code != 401 {
			t.Errorf("%s: %d", name, code)
		}
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	forged := signJWT(other, "x", claims{Iss: "collector", Sub: "collector", Aud: s.app.tokenURL(), Exp: time.Now().Add(30 * time.Second).Unix(), Jti: "j"})
	if code, _ := s.token(t, forged, ""); code != 401 {
		t.Errorf("forged signature: %d", code)
	}
	if code, _ := s.token(t, s.assertion(t, nil), "deploy:request"); code != 403 {
		t.Errorf("unapproved scope: %d", code)
	}
	// alg confusion: an unsigned token is rejected.
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + strings.Split(a, ".")[1] + "."
	if code, _ := s.token(t, none, ""); code != 401 {
		t.Errorf("alg none: %d", code)
	}
	resp, _ := s.ts.Client().Get(s.ts.URL + "/api/v1/.well-known/jwks.json")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"crv":"Ed25519"`) {
		t.Fatalf("jwks: %s", b)
	}
	if strings.Contains(string(b), "signingKey") {
		t.Fatal("private material in JWKS")
	}
}

func TestCatalogPublishNeedsAdminDelegation(t *testing.T) {
	s := newServiceEnv(t)
	_, tok := s.token(t, s.assertion(t, nil), "catalog:publish")
	cat, _ := os.ReadFile("../../examples/catalog.yaml")
	pub := strings.Replace(strings.Replace(string(cat), "revision: 1", "revision: 2", 1), "source: file", "source: collector", 1)

	put := func(deleg string) int {
		req, _ := http.NewRequest("PUT", s.ts.URL+"/api/v1/service/catalog", strings.NewReader(pub))
		req.Header.Set("Authorization", "Bearer "+tok)
		if deleg != "" {
			req.Header.Set("X-Nexus-Delegation", deleg)
		}
		resp, err := s.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put(""); code != 403 {
		t.Fatalf("no delegation: %d", code)
	}
	verify := func(c *client) (int, string) {
		req, _ := http.NewRequest("GET", s.ts.URL+"/api/v1/auth/verify", nil)
		req.Header.Set("X-Nexus-Route", "store")
		resp, err := c.h.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("X-Nexus-Delegation")
	}
	bob, alice := s.client(), s.client()
	if code, _ := verify(bob); code != 401 {
		t.Fatalf("verify anonymous: %d", code)
	}
	bob.login("bob", "bpw")
	alice.login("alice", "apw")
	_, bobDeleg := verify(bob)
	if code := put(bobDeleg); code != 403 {
		t.Fatalf("user delegation accepted: %d", code)
	}
	code, aliceDeleg := verify(alice)
	if code != 200 || aliceDeleg == "" {
		t.Fatalf("verify admin: %d", code)
	}
	if code := put(aliceDeleg); code != 200 {
		t.Fatalf("publish: %d", code)
	}
	if s.app.catalog.Get().Revision != 2 || s.app.catalog.Get().Source != "collector" {
		t.Fatal("catalog not replaced")
	}
	// Revoking the admin's session invalidates the delegation immediately.
	alice.do("POST", "/api/v1/auth/logout", nil)
	if code := put(aliceDeleg); code != 401 {
		t.Fatalf("delegation after logout: %d", code)
	}
	// A user session is not a service token.
	bob.tok = ""
	if code, _ := bob.do("PUT", "/api/v1/service/catalog", nil); code != 401 {
		t.Fatalf("session used as service: %d", code)
	}
}

func TestServiceDisable(t *testing.T) {
	s := newServiceEnv(t)
	_, tok := s.token(t, s.assertion(t, nil), "catalog:publish")
	alice := s.client()
	alice.login("alice", "apw")
	alice.do("POST", "/api/v1/auth/reauth", map[string]string{"password": "apw"})
	if code, _ := alice.do("POST", "/api/v1/admin/services/collector/disable", nil); code != 204 {
		t.Fatalf("disable: %d", code)
	}
	req, _ := http.NewRequest("PUT", s.ts.URL+"/api/v1/service/catalog", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := s.ts.Client().Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("disabled service token accepted: %d", resp.StatusCode)
	}
	if code, _ := s.token(t, s.assertion(t, nil), "catalog:publish"); code != 401 {
		t.Fatalf("disabled service got a token: %d", code)
	}
}
