package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunUsageAndUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("no args: exit %d, want 2", code)
	}
	errb.Reset()
	if code := run([]string{"bogus"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("unknown command: exit %d, stderr %q", code, errb.String())
	}
}

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "knowhere-nexus") {
		t.Fatalf("version: exit %d, out %q", code, out.String())
	}
}

func TestProbeHealthRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{"192.168.1.10:8480", "example.com:80", "garbage"} {
		if err := probeHealth(addr, time.Second); err == nil {
			t.Errorf("probeHealth(%q) = nil, want error", addr)
		}
	}
}

func TestProbeHealthStatus(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	addr := strings.TrimPrefix(ok.URL, "http://")
	if err := probeHealth(addr, time.Second); err != nil {
		t.Fatalf("healthy server: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if err := probeHealth(strings.TrimPrefix(bad.URL, "http://"), time.Second); err == nil {
		t.Fatal("unhealthy server: want error")
	}
}
