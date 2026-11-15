// Package netguard enforces the network boundary (R-15): only configured
// trusted proxies may connect; the client IP is taken from X-Forwarded-For
// only through them and must be a Tailscale address when required.
// Tailscale identity headers are recorded (audit) but never used for
// authorization (Q-07).
package netguard

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

var tailnet4 = netip.MustParsePrefix("100.64.0.0/10")
var tailnet6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// IsTailnet reports whether ip is a Tailscale address.
func IsTailnet(ip netip.Addr) bool {
	ip = ip.Unmap()
	return tailnet4.Contains(ip) || tailnet6.Contains(ip)
}

// Client describes the caller as seen through the boundary.
type Client struct {
	IP             netip.Addr
	TailscaleLogin string
}

type ctxKey struct{}

// ClientFrom returns the caller recorded by Guard.
func ClientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(ctxKey{}).(Client)
	return c
}

// Guard returns the boundary middleware. onReject is called for rejected
// requests (for the security audit) and may be nil.
func Guard(trusted []netip.Addr, requireTailnet bool, onReject func(r *http.Request, reason string)) httpx.Middleware {
	isTrusted := func(a netip.Addr) bool { return slices.Contains(trusted, a.Unmap()) }
	reject := func(w http.ResponseWriter, r *http.Request, reason string) {
		if onReject != nil {
			onReject(r, reason)
		}
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "forbidden")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := peerAddr(r)
			if peer.IsLoopback() && r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if !peer.IsValid() || !isTrusted(peer) {
				reject(w, r, "untrusted_peer")
				return
			}
			client := clientIP(r, peer, isTrusted)
			if requireTailnet && !IsTailnet(client) {
				reject(w, r, "not_tailnet")
				return
			}
			c := Client{IP: client, TailscaleLogin: strings.TrimSpace(r.Header.Get("Tailscale-User-Login"))}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
		})
	}
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// clientIP is the right-most X-Forwarded-For entry that is not a trusted
// proxy, or the peer if there is none.
func clientIP(r *http.Request, peer netip.Addr, trusted func(netip.Addr) bool) netip.Addr {
	var parts []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		parts = append(parts, strings.Split(h, ",")...)
	}
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return netip.Addr{} // malformed chain: treat as unknown
		}
		a = a.Unmap()
		if !trusted(a) {
			return a
		}
	}
	return peer
}
