package httpx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCSP = "default-src 'self'"

func stack(t *testing.T, h http.Handler, logBuf *bytes.Buffer) http.Handler {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(logBuf, nil))
	return Chain(h,
		SecurityHeaders(testCSP),
		RequestID(),
		AccessLog(log),
		Recover(log),
		LimitBody(DefaultLimits.MaxBodyBytes),
	)
}

func assertSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy":      testCSP,
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"X-Frame-Options":              "DENY",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           PermissionsPolicy,
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if h.Get(RequestIDHeader) == "" {
		t.Error("missing request id")
	}
}

func TestHeadersOnEveryStatus(t *testing.T) {
	var logs bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("/err", func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "nope")
	})
	mux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("boom: secret internals") })
	h := stack(t, mux, &logs)
	for _, p := range []string{"/ok", "/err", "/panic", "/missing"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		assertSecurityHeaders(t, rr.Header())
	}
}

func TestPanicIsGeneric(t *testing.T) {
	var logs bytes.Buffer
	h := stack(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: /etc/nexus/secrets.yaml internals")
	}), &logs)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "boom") || strings.Contains(body, "secrets.yaml") || strings.Contains(body, "goroutine") {
		t.Fatalf("panic details leaked: %s", body)
	}
	var env errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code != CodeInternal {
		t.Fatalf("envelope = %s (%v)", body, err)
	}
	if !strings.Contains(logs.String(), "boom") || !strings.Contains(logs.String(), "stack") {
		t.Fatal("panic not logged with stack")
	}
}

func TestRequestIDIgnoresClientValue(t *testing.T) {
	var logs bytes.Buffer
	h := stack(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, RequestIDFrom(r.Context()))
	}), &logs)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(RequestIDHeader, "forged")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		id := rr.Header().Get(RequestIDHeader)
		if id == "forged" || len(id) != 32 || rr.Body.String() != id || seen[id] {
			t.Fatalf("request id %q (body %q)", id, rr.Body.String())
		}
		seen[id] = true
	}
}

func TestAccessLogOmitsQueryAndHeaders(t *testing.T) {
	var logs bytes.Buffer
	h := stack(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}), &logs)
	req := httptest.NewRequest(http.MethodGet, "/path?token=QUERYSECRET", nil)
	req.Header.Set("Authorization", "Bearer HEADERSECRET")
	req.Header.Set("Cookie", "a=COOKIESECRET")
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := logs.String()
	for _, s := range []string{"QUERYSECRET", "HEADERSECRET", "COOKIESECRET"} {
		if strings.Contains(out, s) {
			t.Fatalf("access log leaked %s: %s", s, out)
		}
	}
	if !strings.Contains(out, `"path":"/path"`) {
		t.Fatalf("path not logged: %s", out)
	}
}

func TestBodyLimit(t *testing.T) {
	var logs bytes.Buffer
	h := stack(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if DecodeJSON(w, r, &v) {
			w.WriteHeader(http.StatusNoContent)
		}
	}), &logs)

	big := `{"a":"` + strings.Repeat("x", int(DefaultLimits.MaxBodyBytes)) + `"}`
	// Declared length over the limit.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big)))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversize: %d", rr.Code)
	}
	// Unknown length (chunked): caught while reading.
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversize: %d", rr.Code)
	}
	// Small body passes; unknown fields and trailing data are rejected.
	for body, want := range map[string]int{`{"a":1}`: 204, `{"a":1} {}`: 400, `not json`: 400} {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rr.Code != want {
			t.Errorf("body %q: %d, want %d", body, rr.Code, want)
		}
	}
}

func startServer(t *testing.T, l Limits) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), l)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func TestOversizedHeadersRejected(t *testing.T) {
	addr := startServer(t, DefaultLimits)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	huge := strings.Repeat("a", 32<<10)
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nX-Big: "+huge+"\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status %d, want 431", resp.StatusCode)
	}
}

func TestSlowHeadersCutOff(t *testing.T) {
	l := DefaultLimits
	l.ReadHeaderTimeout = 200 * time.Millisecond
	addr := startServer(t, l)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n") // never finishes headers
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	buf := make([]byte, 1024)
	for {
		if _, err := conn.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("server did not close a slow-header connection")
			}
			break // EOF or reset: connection closed by server
		}
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("slow client held the connection too long")
	}
}
