package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/audit"
	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/netguard"
	"github.com/ntsrihari/knowhere-nexus/internal/session"
)

// Cookie names and paths (R-16).
const (
	accessCookie  = "__Host-nexus_access"
	refreshCookie = "__Secure-nexus_refresh"
	refreshPath   = "/api/v1/auth"
	csrfHeader    = "X-Nexus-CSRF"
)

type sessionKey struct{}

// sessionFrom returns the authenticated session, if any.
func sessionFrom(ctx context.Context) (session.Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(session.Session)
	return s, ok
}

// credential extracts the access token: a bearer header wins over cookies.
func credential(r *http.Request) (token string, bearer bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")), true
	}
	if c, err := r.Cookie(accessCookie); err == nil {
		return c.Value, false
	}
	return "", false
}

// sessionGuard enforces roles with real sessions (replaces M1 PublicOnly).
type sessionGuard struct{ a *App }

func (g sessionGuard) Enforce(role authz.Role, next http.Handler) http.Handler {
	if role == authz.Public {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(string(role), "service:") {
			g.a.serviceGuard(role, next).ServeHTTP(w, r)
			return
		}
		tok, _ := credential(r)
		sess, err := g.a.sessions.Authenticate(r.Context(), tok)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthenticated, "authentication required")
			return
		}
		now := g.a.now()
		switch role {
		case authz.User:
		case authz.Admin, authz.AdminFresh:
			if !sess.IsAdmin(now) {
				g.a.deny(r, sess, "admin_required")
				httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "administrator access required")
				return
			}
		case authz.AdminFreshReauth:
			if !sess.IsAdmin(now) {
				g.a.deny(r, sess, "admin_required")
				httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "administrator access required")
				return
			}
			if !sess.RecentlyReauthenticated(now) {
				httpx.WriteError(w, http.StatusForbidden, httpx.CodeReauthRequired, "confirm your password to continue")
				return
			}
		default:
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

// deny audits a denied administrative attempt (R-11).
func (a *App) deny(r *http.Request, s session.Session, reason string) {
	_ = a.audit.Write(r.Context(), a.entry(r, &s, "authz.denied", r.Method+" "+r.URL.Path, reason, nil))
}

// entry builds an audit entry for the current request.
func (a *App) entry(r *http.Request, s *session.Session, action, target, result string, params map[string]any) audit.Entry {
	e := audit.Entry{Action: action, Target: target, Result: result, Params: params,
		RequestID: httpx.RequestIDFrom(r.Context()), TailscaleLogin: netguard.ClientFrom(r.Context()).TailscaleLogin}
	if s != nil {
		e.ActorUserID, e.ActorName = s.UserID, s.Username
	}
	return e
}

// writeAudit records an admin action; a failure aborts the action (R-11).
func (a *App) writeAudit(w http.ResponseWriter, r *http.Request, e audit.Entry) bool {
	if err := a.audit.Write(r.Context(), e); err != nil {
		a.log.Error("audit write failed", "action", e.Action, "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "audit log unavailable; action not performed")
		return false
	}
	return true
}

// csrf implements R-16 for every unsafe method not using a bearer token.
func csrf(origin string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if _, bearer := credential(r); bearer {
				next.ServeHTTP(w, r)
				return
			}
			ok := r.Header.Get(csrfHeader) == "1"
			if o := r.Header.Get("Origin"); o != "" {
				ok = ok && o == origin
			} else if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
				ok = ok && site == "same-origin"
			} else if r.Header.Get("Cookie") != "" {
				// A browser carrying cookies but no origin information.
				ok = false
			}
			if !ok {
				httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "cross-site request rejected")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setSessionCookies(w http.ResponseWriter, t session.Tokens, now time.Time) {
	secs := func(until time.Time) int { return int(until.Sub(now).Round(time.Second).Seconds()) }
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: t.Access, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: secs(t.AccessExpires)})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: t.Refresh, Path: refreshPath, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: secs(t.AbsoluteExpires)})
}

func clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: "", Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: refreshPath, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

var errNoSession = errors.New("no session in context")

func mustSession(r *http.Request) session.Session {
	s, ok := sessionFrom(r.Context())
	if !ok {
		panic(errNoSession) // route table guarantees a session; Recover answers 500
	}
	return s
}
