package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/jellyfin"
	"github.com/ntsrihari/knowhere-nexus/internal/netguard"
	"github.com/ntsrihari/knowhere-nexus/internal/session"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

type userView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type sessionView struct {
	User              userView  `json:"user"`
	Role              string    `json:"role"`
	RoleVerifiedAt    time.Time `json:"roleVerifiedAt"`
	AbsoluteExpiresAt time.Time `json:"absoluteExpiresAt"`
	AuthMethod        string    `json:"authMethod"`
	TailscaleLogin    *string   `json:"tailscaleLogin"`
}

func (a *App) view(s session.Session) sessionView {
	role := s.EffectiveRole(a.now())
	v := sessionView{User: userView{ID: s.UserID, Name: s.Username, Role: role}, Role: role,
		RoleVerifiedAt: s.RoleVerifiedAt, AbsoluteExpiresAt: s.AbsoluteExpiresAt, AuthMethod: s.AuthMethod}
	if s.TailscaleLogin != "" {
		t := s.TailscaleLogin
		v.TailscaleLogin = &t
	}
	return v
}

// authLimited applies the login rate limits (SECURITY.md §3).
func (a *App) authLimited(w http.ResponseWriter, r *http.Request, user string) bool {
	ip := netguard.ClientFrom(r.Context()).IP.String()
	if !a.ipLimit.Allowed(ip) || (user != "" && !a.userLimit.Allowed(strings.ToLower(user))) {
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts; try again later")
		return true
	}
	return false
}

func (a *App) authFailed(r *http.Request, user string) {
	a.ipLimit.Fail(netguard.ClientFrom(r.Context()).IP.String())
	if user != "" {
		a.userLimit.Fail(strings.ToLower(user))
	}
}

// writeUpstreamError maps Jellyfin failures to client responses.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jellyfin.ErrInvalidCredentials):
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "invalid username or password")
	case errors.Is(err, upstream.ErrNotConfigured):
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotConfigured, "Jellyfin is not configured")
	default:
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodeUpstream, "Jellyfin is unavailable")
	}
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, id jellyfin.Identity, kind, method, device string) (session.Tokens, session.Session, bool) {
	toks, sess, err := a.sessions.Create(r.Context(), session.New{
		UserID: id.UserID, Username: id.Name, IsAdmin: id.IsAdmin, Kind: kind, AuthMethod: method,
		DeviceLabel: device, TailscaleLogin: netguard.ClientFrom(r.Context()).TailscaleLogin,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not create session")
		return toks, sess, false
	}
	_ = a.audit.Write(r.Context(), a.entry(r, &sess, "auth.login", sess.FamilyID, "ok", map[string]any{"method": method, "kind": kind}))
	return toks, sess, true
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" || len(body.Username) > 128 || len(body.Password) > 1024 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "username and password are required")
		return
	}
	if a.authLimited(w, r, body.Username) {
		return
	}
	id, err := a.jf.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, jellyfin.ErrInvalidCredentials) {
			a.authFailed(r, body.Username)
			_ = a.audit.Write(r.Context(), a.entry(r, nil, "auth.login", "", "failed", map[string]any{"username": body.Username}))
		}
		writeAuthError(w, err)
		return
	}
	a.userLimit.Reset(strings.ToLower(body.Username))
	toks, sess, ok := a.startSession(w, r, id, session.KindBrowser, "password", "")
	if !ok {
		return
	}
	setSessionCookies(w, toks, a.now())
	httpx.WriteJSON(w, http.StatusOK, a.view(sess))
}

// pendingQC holds Quick Connect requests; the Jellyfin secret never leaves Nexus.
type pendingQC struct {
	mu sync.Mutex
	m  map[string]qcEntry
}

type qcEntry struct {
	qc      jellyfin.QuickConnect
	expires time.Time
}

func randomID() string {
	var b [18]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (p *pendingQC) put(qc jellyfin.QuickConnect, now time.Time) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
	if len(p.m) >= 1000 {
		return "", false
	}
	id := randomID()
	p.m[id] = qcEntry{qc: qc, expires: now.Add(5 * time.Minute)}
	return id, true
}

func (p *pendingQC) get(id string, now time.Time) (jellyfin.QuickConnect, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[id]
	if !ok || now.After(e.expires) {
		delete(p.m, id)
		return jellyfin.QuickConnect{}, false
	}
	return e.qc, true
}

func (p *pendingQC) drop(id string) { p.mu.Lock(); delete(p.m, id); p.mu.Unlock() }

func (a *App) handleQuickConnectStart(w http.ResponseWriter, r *http.Request) {
	if a.authLimited(w, r, "") {
		return
	}
	qc, err := a.jf.StartQuickConnect(r.Context())
	if err != nil {
		writeAuthError(w, err)
		return
	}
	id, ok := a.qc.put(qc, a.now())
	if !ok {
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many pending requests")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"code": qc.Code, "pollId": id})
}

// handleQuickConnectPoll is POST because a successful poll creates a session.
func (a *App) handleQuickConnectPoll(w http.ResponseWriter, r *http.Request) {
	pollID := r.PathValue("pollId")
	qc, ok := a.qc.get(pollID, a.now())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "expired"})
		return
	}
	id, done, err := a.jf.PollQuickConnect(r.Context(), qc)
	if err != nil {
		if errors.Is(err, jellyfin.ErrInvalidCredentials) {
			a.qc.drop(pollID)
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "expired"})
			return
		}
		writeAuthError(w, err)
		return
	}
	if !done {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "pending"})
		return
	}
	a.qc.drop(pollID)
	toks, sess, ok := a.startSession(w, r, id, session.KindBrowser, "quickconnect", "")
	if !ok {
		return
	}
	setSessionCookies(w, toks, a.now())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"state": "authorized", "session": a.view(sess)})
}

func (a *App) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JellyfinToken string `json:"jellyfinToken"`
		DeviceLabel   string `json:"deviceLabel"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if body.JellyfinToken == "" || len(body.JellyfinToken) > 256 || len(body.DeviceLabel) > 64 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "jellyfinToken is required")
		return
	}
	if a.authLimited(w, r, "") {
		return
	}
	id, err := a.jf.Validate(r.Context(), body.JellyfinToken)
	if err != nil {
		if errors.Is(err, jellyfin.ErrInvalidCredentials) {
			a.authFailed(r, "")
		}
		writeAuthError(w, err)
		return
	}
	toks, sess, ok := a.startSession(w, r, id, session.KindMilano, "exchange", body.DeviceLabel)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accessToken": toks.Access, "refreshToken": toks.Refresh,
		"accessExpiresAt": toks.AccessExpires, "absoluteExpiresAt": toks.AbsoluteExpires, "user": a.view(sess).User})
}

func (a *App) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	fromBody := false
	if r.ContentLength != 0 && r.Header.Get("Content-Type") == "application/json" {
		if !httpx.DecodeJSON(w, r, &body) {
			return
		}
		fromBody = body.RefreshToken != ""
	}
	tok := body.RefreshToken
	if !fromBody {
		if c, err := r.Cookie(refreshCookie); err == nil {
			tok = c.Value
		}
	}
	toks, sess, err := a.sessions.Refresh(r.Context(), tok)
	if err != nil {
		if errors.Is(err, session.ErrReuse) {
			_ = a.audit.Write(r.Context(), a.entry(r, nil, "auth.refresh_reuse", "", "revoked", nil))
		}
		if !fromBody {
			clearSessionCookies(w)
		}
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "session expired; sign in again")
		return
	}
	if fromBody {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"accessToken": toks.Access, "refreshToken": toks.Refresh,
			"accessExpiresAt": toks.AccessExpires, "absoluteExpiresAt": toks.AbsoluteExpires, "user": a.view(sess).User})
		return
	}
	setSessionCookies(w, toks, a.now())
	httpx.WriteJSON(w, http.StatusOK, a.view(sess))
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	if err := a.sessions.Revoke(r.Context(), s.FamilyID, s.UserID, "logout"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "logout failed")
		return
	}
	a.events.revoke(s.FamilyID)
	_ = a.audit.Write(r.Context(), a.entry(r, &s, "auth.logout", s.FamilyID, "ok", nil))
	clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, a.view(mustSession(r)))
}

func (a *App) handleReauth(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	var body struct {
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if body.Password == "" || len(body.Password) > 1024 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "password is required")
		return
	}
	if a.authLimited(w, r, s.Username) {
		return
	}
	id, err := a.jf.Login(r.Context(), s.Username, body.Password)
	if err == nil && id.UserID != s.UserID {
		err = jellyfin.ErrInvalidCredentials
	}
	if err != nil {
		if errors.Is(err, jellyfin.ErrInvalidCredentials) {
			a.authFailed(r, s.Username)
			_ = a.audit.Write(r.Context(), a.entry(r, &s, "auth.reauth", s.FamilyID, "failed", nil))
		}
		writeAuthError(w, err)
		return
	}
	if err := a.sessions.MarkReauth(r.Context(), s.FamilyID, id.IsAdmin); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not update session")
		return
	}
	_ = a.audit.Write(r.Context(), a.entry(r, &s, "auth.reauth", s.FamilyID, "ok", map[string]any{"admin": id.IsAdmin}))
	w.WriteHeader(http.StatusNoContent)
}

type sessionItem struct {
	session.Session
	Current bool `json:"current"`
}

func (a *App) listSessions(w http.ResponseWriter, r *http.Request, userID string, current string) {
	list, err := a.sessions.List(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not list sessions")
		return
	}
	out := make([]sessionItem, 0, len(list))
	for _, s := range list {
		out = append(out, sessionItem{Session: s, Current: s.FamilyID == current})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) handleMySessions(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	a.listSessions(w, r, s.UserID, s.FamilyID)
}

func (a *App) handleRevokeMySession(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	target, err := a.sessions.Get(r.Context(), r.PathValue("id"))
	if err != nil || target.UserID != s.UserID {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "session.revoke", target.FamilyID, "ok", map[string]any{"scope": "own"})) {
		return
	}
	if err := a.sessions.Revoke(r.Context(), target.FamilyID, s.UserID, "user_revoke"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "revoke failed")
		return
	}
	a.events.revoke(target.FamilyID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	if !a.writeAudit(w, r, a.entry(r, &s, "session.revoke_others", s.UserID, "ok", nil)) {
		return
	}
	ids, err := a.sessions.RevokeWhere(r.Context(), s.UserID, s.FamilyID, s.UserID, "user_revoke_others")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "revoke failed")
		return
	}
	for _, id := range ids {
		a.events.revoke(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	a.listSessions(w, r, r.URL.Query().Get("user"), mustSession(r).FamilyID)
}

func (a *App) handleAdminRevokeSession(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	target, err := a.sessions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "session.admin_revoke", target.FamilyID, "ok", map[string]any{"user": target.UserID})) {
		return
	}
	if err := a.sessions.Revoke(r.Context(), target.FamilyID, s.UserID, "admin_revoke"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "revoke failed")
		return
	}
	a.events.revoke(target.FamilyID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleAdminRevokeUser(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	user := r.PathValue("userId")
	if !a.writeAudit(w, r, a.entry(r, &s, "session.admin_revoke_user", user, "ok", nil)) {
		return
	}
	ids, err := a.sessions.RevokeWhere(r.Context(), user, "", s.UserID, "admin_revoke_user")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "revoke failed")
		return
	}
	for _, id := range ids {
		a.events.revoke(id)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"revoked": len(ids)})
}

func (a *App) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var before int64
	if v := q.Get("before"); v != "" {
		for _, c := range v {
			if c < '0' || c > '9' {
				httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "before must be a number")
				return
			}
			before = before*10 + int64(c-'0')
			if before > 1<<53 {
				break
			}
		}
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' || n > 1000 {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n <= 0 || n > 200 {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "limit must be 1-200")
			return
		}
		limit = n
	}
	list, err := a.audit.List(r.Context(), before, limit, q.Get("actor"), q.Get("action"))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "audit unavailable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *App) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	broken, err := a.audit.Verify(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "audit unavailable")
		return
	}
	var b *int64
	if broken != 0 {
		b = &broken
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": broken == 0, "brokenAt": b})
}
