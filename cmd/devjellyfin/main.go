// Command devjellyfin runs the in-memory fake Jellyfin used by tests, for
// local development and browser end-to-end checks. It is never part of the
// container image. Users: admin/admin (administrator), family/family.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/ntsrihari/knowhere-nexus/internal/testutil"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18096", "listen address (loopback only)")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		fmt.Fprintln(os.Stderr, "devjellyfin listens on loopback only")
		os.Exit(2)
	}
	f := testutil.NewFakeJellyfin(
		testutil.FakeUser{ID: "a0000000000000000000000000000001", Name: "admin", Password: "admin", Admin: true},
		testutil.FakeUser{ID: "f0000000000000000000000000000002", Name: "family", Password: "family"},
	)
	f.Close() // stop the httptest listener; serve the same handler on addr
	fmt.Printf("fake Jellyfin on http://%s\n", *addr)
	srv := &http.Server{Addr: *addr, Handler: f.Config.Handler}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
