package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// defaultHealthAddr is the in-container listen address used by the image.
const defaultHealthAddr = "127.0.0.1:8480"

// healthcheckCmd probes GET /healthz on a loopback address. It is used by the
// container HEALTHCHECK, so it must not depend on config or secrets.
func healthcheckCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", defaultHealthAddr, "loopback host:port of the Nexus listener")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := probeHealth(*addr, 3*time.Second); err != nil {
		fmt.Fprintf(stderr, "unhealthy: %v\n", err)
		return 1
	}
	return 0
}

func probeHealth(addr string, timeout time.Duration) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid -addr: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr must be a loopback IP address, got %q", host)
	}
	client := &http.Client{
		Timeout: timeout,
		// Explicit transport: never use HTTP_PROXY/HTTPS_PROXY for a local probe.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
