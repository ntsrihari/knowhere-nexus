// Package testutil contains helpers shared by tests.
package testutil

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
)

// ErrNetworkDisabled is returned by every outbound dial made through the
// default transport or resolver while the guard is active.
var ErrNetworkDisabled = errors.New("outbound network disabled in tests")

// RunWithoutNetwork replaces http.DefaultTransport and net.DefaultResolver
// with versions that fail every dial, runs the tests, and exits (ST-11).
// Tests talk to servers under test through explicitly constructed clients.
func RunWithoutNetwork(m *testing.M) {
	fail := func(context.Context, string, string) (net.Conn, error) { return nil, ErrNetworkDisabled }
	http.DefaultTransport = &http.Transport{DialContext: fail, Proxy: nil}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: fail}
	os.Exit(m.Run())
}

// LocalClient returns a client for servers under test that bypasses the guard
// and any proxy settings.
func LocalClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil}}
}
