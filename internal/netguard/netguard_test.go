package netguard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func run(trusted []string, requireTailnet bool, remote, path string, hdr map[string]string) (int, Client) {
	var ts []netip.Addr
	for _, s := range trusted {
		ts = append(ts, netip.MustParseAddr(s))
	}
	var got Client
	h := Guard(ts, requireTailnet, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClientFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, got
}

func TestGuard(t *testing.T) {
	caddy := []string{"172.30.0.2"}
	cases := []struct {
		name    string
		trusted []string
		tailnet bool
		remote  string
		path    string
		hdr     map[string]string
		want    int
		ip      string
	}{
		{"healthz from loopback", nil, true, "127.0.0.1:1", "/healthz", nil, 200, ""},
		{"healthz from elsewhere", nil, true, "10.0.0.9:1", "/healthz", nil, 403, ""},
		{"untrusted peer", caddy, true, "10.0.0.9:1", "/", nil, 403, ""},
		{"trusted, tailnet client", caddy, true, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "100.101.2.3"}, 200, "100.101.2.3"},
		{"trusted, LAN client", caddy, true, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "192.168.29.10"}, 403, ""},
		{"spoofed left entries ignored", caddy, true, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "100.64.0.1, 8.8.8.8"}, 403, ""},
		{"no XFF uses peer", caddy, true, "172.30.0.2:1", "/", nil, 403, ""},
		{"dev mode", []string{"127.0.0.1"}, false, "127.0.0.1:5", "/", nil, 200, "127.0.0.1"},
		{"malformed XFF", caddy, false, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "nonsense"}, 200, "invalid IP"},
		{"tailscale v6", caddy, true, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "fd7a:115c:a1e0::5"}, 200, "fd7a:115c:a1e0::5"},
	}
	for _, c := range cases {
		code, cl := run(c.trusted, c.tailnet, c.remote, c.path, c.hdr)
		if code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
		if c.want == 200 && c.ip != "" && cl.IP.String() != c.ip {
			t.Errorf("%s: client %s, want %s", c.name, cl.IP, c.ip)
		}
	}
	_, cl := run(caddy, true, "172.30.0.2:1", "/", map[string]string{"X-Forwarded-For": "100.64.0.9", "Tailscale-User-Login": "sri@example.com"})
	if cl.TailscaleLogin != "sri@example.com" {
		t.Fatal("tailscale login not recorded")
	}
}
