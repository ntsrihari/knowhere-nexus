package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBreakerOpensAndRecovers(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(502)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New("t", srv.URL)
	now := time.Now()
	c.now = func() time.Time { return now }
	for i := 0; i < BreakAfter; i++ {
		if err := c.Do(context.Background(), Request{Path: "/x"}, nil); err == nil {
			t.Fatal("expected failure")
		}
	}
	if err := c.Do(context.Background(), Request{Path: "/x"}, nil); !errors.Is(err, ErrOpen) {
		t.Fatalf("breaker not open: %v", err)
	}
	if c.Status().Breaker != Open {
		t.Fatal("status not open")
	}
	now = now.Add(RetryAfter)
	fail = false
	var out map[string]bool
	if err := c.Do(context.Background(), Request{Path: "/x"}, &out); err != nil || !out["ok"] {
		t.Fatalf("half-open probe: %v %v", err, out)
	}
	if st := c.Status(); st.Breaker != Closed || st.LastSuccessAt == nil {
		t.Fatalf("not recovered: %+v", st)
	}
}

func TestClientErrorsDoNotTripBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	c := New("t", srv.URL)
	for i := 0; i < BreakAfter+2; i++ {
		err := c.Do(context.Background(), Request{Path: "/x"}, nil)
		if !IsStatus(err, 401) {
			t.Fatalf("want 401, got %v", err)
		}
	}
	if c.Status().Breaker != Closed {
		t.Fatal("4xx tripped the breaker")
	}
}

func TestSizeCapAndNotConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxResponse+10)))
	}))
	defer srv.Close()
	if _, err := New("t", srv.URL).DoRaw(context.Background(), Request{Path: "/"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
	if _, err := New("t", "").DoRaw(context.Background(), Request{Path: "/"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("not configured: %v", err)
	}
	if _, err := New("t", srv.URL).DoRaw(context.Background(), Request{Path: "relative"}); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestRedirectsNotFollowed(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusFound))
	defer srv.Close()
	b, err := New("t", srv.URL).DoRaw(context.Background(), Request{Path: "/"})
	if strings.Contains(string(b), "secret") || !IsStatus(err, 302) {
		t.Fatalf("redirect followed: %q %v", b, err)
	}
}
