package app

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/config"
	"github.com/ntsrihari/knowhere-nexus/internal/contract"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/session"
)

// Token lifetimes (R-03).
const (
	serviceTokenTTL = 5 * time.Minute
	delegationTTL   = 120 * time.Second
	assertionMaxTTL = 60 * time.Second
	clockSkew       = 30 * time.Second
	issuer          = "nexus"
)

// approvedScopes are the scopes a service may be granted. deploy:request is
// reserved and not approved (A-03, D-16).
var approvedScopes = []string{"catalog:publish", "themes:publish"}

type claims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
	Jti   string `json:"jti"`
	Scope string `json:"scope,omitempty"`
	Role  string `json:"role,omitempty"`
	Sid   string `json:"sid,omitempty"`
}

type tokenKit struct {
	priv ed25519.PrivateKey
	kid  string

	mu       sync.Mutex
	seenJTI  map[string]time.Time // assertion replay protection
	denied   map[string]time.Time // revoked service-token jtis
	disabled map[string]bool      // runtime-disabled services
}

func (a *App) tokens() *tokenKit {
	a.tokOnce.Do(func() {
		t := &tokenKit{seenJTI: map[string]time.Time{}, denied: map[string]time.Time{}, disabled: map[string]bool{}}
		if !a.secrets.SigningKey.IsZero() {
			t.priv = ed25519.NewKeyFromSeed(a.secrets.SigningKey.Expose())
			sum := sha256.Sum256(t.priv.Public().(ed25519.PublicKey))
			t.kid = hex.EncodeToString(sum[:8])
		}
		a.tok = t
	})
	return a.tok
}

var b64 = base64.RawURLEncoding

func signJWT(priv ed25519.PrivateKey, kid string, c claims) string {
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": kid})
	p, _ := json.Marshal(c)
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	return in + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(in)))
}

// parseJWT verifies an EdDSA JWT with pub and returns its claims. Any other
// algorithm is rejected (alg pinned).
func parseJWT(tok string, pub ed25519.PublicKey) (claims, error) {
	var c claims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || len(tok) > 4096 {
		return c, errors.New("malformed token")
	}
	hb, err1 := b64.DecodeString(parts[0])
	pb, err2 := b64.DecodeString(parts[1])
	sig, err3 := b64.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return c, errors.New("malformed token")
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hb, &h) != nil || h.Alg != "EdDSA" {
		return c, errors.New("unsupported algorithm")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return c, errors.New("bad signature")
	}
	if json.Unmarshal(pb, &c) != nil {
		return c, errors.New("malformed claims")
	}
	return c, nil
}

func (a *App) service(id string) (config.Service, bool) {
	for _, s := range a.cfg.Services {
		if s.ID == id {
			t := a.tokens()
			t.mu.Lock()
			dis := t.disabled[id]
			t.mu.Unlock()
			return s, !s.Disabled && !dis
		}
	}
	return config.Service{}, false
}

func (a *App) tokenURL() string { return a.cfg.PublicOrigin + "/api/v1/service/token" }

// handleServiceToken implements client credentials with private_key_jwt.
func (a *App) handleServiceToken(w http.ResponseWriter, r *http.Request) {
	t := a.tokens()
	if t.priv == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotConfigured, "service tokens are not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid form")
		return
	}
	f := r.PostForm
	if f.Get("grant_type") != "client_credentials" ||
		f.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "unsupported grant")
		return
	}
	assertion := f.Get("client_assertion")
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "invalid client assertion")
		return
	}
	var unverified claims
	if pb, err := b64.DecodeString(parts[1]); err != nil || json.Unmarshal(pb, &unverified) != nil {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "invalid client assertion")
		return
	}
	svc, ok := a.service(unverified.Iss)
	now := a.now()
	fail := func(reason string) {
		_ = a.audit.Write(r.Context(), a.entry(r, nil, "service.token", unverified.Iss, "refused", map[string]any{"reason": reason}))
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "invalid client assertion")
	}
	if !ok {
		fail("unknown_or_disabled_service")
		return
	}
	c, err := parseJWT(assertion, svc.PublicKey)
	if err != nil {
		fail("signature")
		return
	}
	exp := time.Unix(c.Exp, 0)
	if c.Iss != svc.ID || c.Sub != svc.ID || c.Aud != a.tokenURL() || c.Jti == "" ||
		now.After(exp.Add(clockSkew)) || exp.Sub(now) > assertionMaxTTL+clockSkew {
		fail("claims")
		return
	}
	t.mu.Lock()
	for j, e := range t.seenJTI {
		if now.After(e) {
			delete(t.seenJTI, j)
		}
	}
	_, replay := t.seenJTI[svc.ID+"|"+c.Jti]
	t.seenJTI[svc.ID+"|"+c.Jti] = exp.Add(clockSkew)
	t.mu.Unlock()
	if replay {
		fail("replay")
		return
	}
	requested := strings.Fields(f.Get("scope"))
	for _, s := range requested {
		if !slices.Contains(svc.Scopes, s) || !slices.Contains(approvedScopes, s) {
			_ = a.audit.Write(r.Context(), a.entry(r, nil, "service.token", svc.ID, "refused", map[string]any{"scope": s}))
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "scope not allowed: "+s)
			return
		}
	}
	tok := signJWT(t.priv, t.kid, claims{Iss: issuer, Sub: "svc:" + svc.ID, Aud: issuer, Iat: now.Unix(),
		Exp: now.Add(serviceTokenTTL).Unix(), Jti: randomID(), Scope: strings.Join(requested, " ")})
	_ = a.audit.Write(r.Context(), a.entry(r, nil, "service.token", svc.ID, "issued", map[string]any{"scope": strings.Join(requested, " ")}))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"access_token": tok, "token_type": "Bearer",
		"expires_in": int(serviceTokenTTL.Seconds()), "scope": strings.Join(requested, " ")})
}

func (a *App) handleJWKS(w http.ResponseWriter, r *http.Request) {
	t := a.tokens()
	keys := []map[string]string{}
	if t.priv != nil {
		keys = append(keys, map[string]string{"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig", "kid": t.kid,
			"x": b64.EncodeToString(t.priv.Public().(ed25519.PublicKey))})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

type serviceKey struct{}

type serviceCaller struct {
	ID       string
	Scopes   []string
	Delegate *session.Session
}

// serviceAuth enforces service:<scope> roles: a valid Nexus service token
// for an enabled service with the scope. Delegated user context, when sent,
// is verified and the session must still be live (R-03).
func (a *App) serviceAuth(role authz.Role, next http.Handler) http.Handler {
	scope := strings.TrimPrefix(string(role), "service:")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := a.tokens()
		deny := func(code int, msg string) {
			httpx.WriteError(w, code, httpx.CodeUnauthenticated, msg)
		}
		tok, bearer := credential(r)
		if t.priv == nil || !bearer {
			deny(http.StatusUnauthorized, "service authentication required")
			return
		}
		c, err := parseJWT(tok, t.priv.Public().(ed25519.PublicKey))
		now := a.now()
		if err != nil || c.Iss != issuer || c.Aud != issuer || !strings.HasPrefix(c.Sub, "svc:") ||
			now.After(time.Unix(c.Exp, 0).Add(clockSkew)) {
			deny(http.StatusUnauthorized, "invalid service token")
			return
		}
		id := strings.TrimPrefix(c.Sub, "svc:")
		t.mu.Lock()
		_, revoked := t.denied[c.Jti]
		t.mu.Unlock()
		if _, ok := a.service(id); !ok || revoked {
			deny(http.StatusUnauthorized, "service disabled or token revoked")
			return
		}
		if !slices.Contains(strings.Fields(c.Scope), scope) {
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "scope required: "+scope)
			return
		}
		caller := serviceCaller{ID: id, Scopes: strings.Fields(c.Scope)}
		if d := r.Header.Get("X-Nexus-Delegation"); d != "" {
			dc, err := parseJWT(d, t.priv.Public().(ed25519.PublicKey))
			if err != nil || dc.Iss != issuer || dc.Aud != id || now.After(time.Unix(dc.Exp, 0).Add(clockSkew)) {
				deny(http.StatusUnauthorized, "invalid delegation token")
				return
			}
			sess, err := a.sessions.Get(r.Context(), dc.Sid)
			if err != nil || "user:"+sess.UserID != dc.Sub {
				deny(http.StatusUnauthorized, "delegated session is no longer active")
				return
			}
			caller.Delegate = &sess
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serviceKey{}, caller)))
	})
}

// handleVerify is the forward-auth check for authorized routes (A-02, R-06).
func (a *App) handleVerify(w http.ResponseWriter, r *http.Request) {
	routeID := r.Header.Get("X-Nexus-Route")
	var route *config.Route
	for i := range a.cfg.Routes {
		if a.cfg.Routes[i].ID == routeID && a.cfg.Routes[i].AuthMode == "nexus" {
			route = &a.cfg.Routes[i]
		}
	}
	if route == nil {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "route not authorized")
		return
	}
	tok, _ := credential(r)
	sess, err := a.sessions.Authenticate(r.Context(), tok)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "sign in required")
		return
	}
	role := sess.EffectiveRole(a.now())
	w.Header().Set("X-Nexus-User-Id", sess.UserID)
	w.Header().Set("X-Nexus-User-Name", sess.Username)
	w.Header().Set("X-Nexus-Role", role)
	if t := a.tokens(); t.priv != nil && route.Audience != "" {
		now := a.now()
		w.Header().Set("X-Nexus-Delegation", signJWT(t.priv, t.kid, claims{Iss: issuer, Sub: "user:" + sess.UserID,
			Aud: route.Audience, Iat: now.Unix(), Exp: now.Add(delegationTTL).Unix(), Jti: randomID(), Role: role, Sid: sess.FamilyID}))
	}
	w.WriteHeader(http.StatusOK)
}

// handleServiceCatalog lets Collector publish the catalog (A-06): requires
// catalog:publish and an admin delegation from a live session.
func (a *App) handleServiceCatalog(w http.ResponseWriter, r *http.Request) {
	caller, _ := r.Context().Value(serviceKey{}).(serviceCaller)
	if caller.Delegate == nil || !caller.Delegate.IsAdmin(a.now()) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "an administrator delegation is required")
		return
	}
	b, ok := readSmall(w, r, contract.CatalogMaxSize)
	if !ok {
		return
	}
	c, err := contract.ParseCatalog(b, a.catalogOptions())
	if err == nil && c.Source != "collector" {
		err = errors.New("source must be collector")
	}
	if err == nil {
		err = a.catalog.Replace(c)
	}
	s := caller.Delegate
	if err != nil {
		_ = a.audit.Write(r.Context(), a.entry(r, s, "catalog.publish", caller.ID, "rejected", map[string]any{"error": err.Error()}))
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, err.Error())
		return
	}
	if !a.writeAudit(w, r, a.entry(r, s, "catalog.publish", caller.ID, "ok", map[string]any{"revision": c.Revision})) {
		return
	}
	a.events.publish("catalog.changed", map[string]int64{"revision": c.Revision}, false)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"revision": c.Revision, "apps": len(c.Apps)})
}

func (a *App) handleAdminServices(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, s := range a.cfg.Services {
		_, enabled := a.service(s.ID)
		out = append(out, map[string]any{"id": s.ID, "scopes": s.Scopes, "enabled": enabled})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) handleServiceToggle(enable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := mustSession(r)
		id := r.PathValue("id")
		found := false
		for _, sv := range a.cfg.Services {
			found = found || sv.ID == id
		}
		if !found {
			httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "unknown service")
			return
		}
		action := "service.disable"
		if enable {
			action = "service.enable"
		}
		if !a.writeAudit(w, r, a.entry(r, &s, action, id, "ok", nil)) {
			return
		}
		t := a.tokens()
		t.mu.Lock()
		t.disabled[id] = !enable
		t.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) handleRevokeServiceToken(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	var body struct {
		JTI string `json:"jti"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if body.JTI == "" || len(body.JTI) > 64 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "jti required")
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "service.token.revoke", body.JTI, "ok", nil)) {
		return
	}
	t := a.tokens()
	t.mu.Lock()
	t.denied[body.JTI] = a.now().Add(serviceTokenTTL + clockSkew)
	t.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) registerServiceRoutes() {
	get, post := []string{"GET"}, []string{"POST"}
	R := a.routes.Handle
	R("/api/v1/.well-known/jwks.json", authz.Public, get, http.HandlerFunc(a.handleJWKS))
	R("/api/v1/service/token", authz.Public, post, http.HandlerFunc(a.handleServiceToken))
	R("/api/v1/auth/verify", authz.Public, get, http.HandlerFunc(a.handleVerify))
	R("/api/v1/service/catalog", authz.Service("catalog:publish"), []string{"PUT"}, http.HandlerFunc(a.handleServiceCatalog))
	R("/api/v1/admin/services", authz.Admin, get, http.HandlerFunc(a.handleAdminServices))
	R("/api/v1/admin/services/{id}/disable", authz.AdminFreshReauth, post, a.handleServiceToggle(false))
	R("/api/v1/admin/services/{id}/enable", authz.AdminFreshReauth, post, a.handleServiceToggle(true))
	R("/api/v1/admin/service-tokens/revoke", authz.AdminFresh, post, http.HandlerFunc(a.handleRevokeServiceToken))
}
