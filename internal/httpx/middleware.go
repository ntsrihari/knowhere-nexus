package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const requestIDKey ctxKey = 1

// RequestIDHeader is the response header carrying the request id.
const RequestIDHeader = "X-Request-ID"

// RequestID assigns a fresh random id to every request. Client-supplied ids
// are ignored so they cannot be used to forge log correlation.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newID()
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// SecurityHeaders sets the response headers from SECURITY.md §4.7 on every
// response, before the handler runs, so error and panic responses carry them.
func SecurityHeaders(csp string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", PermissionsPolicy)
			next.ServeHTTP(w, r)
		})
	}
}

// PermissionsPolicy disables browser features Nexus does not use.
const PermissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), " +
	"encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), magnetometer=(), " +
	"microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), " +
	"screen-wake-lock=(), usb=(), xr-spatial-tracking=()"

// Recover turns a panic into a generic 500 envelope. The stack trace goes to
// the log only; nothing about the failure is sent to the client.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("handler panic",
					"panic", fmt.Sprint(v),
					"request_id", RequestIDFrom(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()))
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// LimitBody rejects declared oversized bodies with 413 and caps the rest.
func LimitBody(max int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "request body too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request. It records the path only, never the
// query string, headers or bodies, which may carry credentials.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			defer func() {
				status := rec.status
				if status == 0 {
					status = http.StatusOK
				}
				log.Info("request",
					"request_id", RequestIDFrom(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"status", status,
					"bytes", rec.bytes,
					"duration_ms", time.Since(start).Milliseconds())
			}()
			next.ServeHTTP(rec, r)
		})
	}
}
