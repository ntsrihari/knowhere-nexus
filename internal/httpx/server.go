package httpx

import (
	"net/http"
	"time"
)

// Limits are the server-wide resource limits (SECURITY.md §4.7).
type Limits struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

// DefaultLimits are used in production.
var DefaultLimits = Limits{
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       15 * time.Second,
	WriteTimeout:      30 * time.Second,
	IdleTimeout:       60 * time.Second,
	MaxHeaderBytes:    16 << 10,
	MaxBodyBytes:      1 << 20,
}

// NewServer returns an http.Server with the given limits applied.
// WriteTimeout also bounds streaming responses; SSE (M3) must manage its own
// deadline with http.ResponseController.
func NewServer(addr string, h http.Handler, l Limits) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: l.ReadHeaderTimeout,
		ReadTimeout:       l.ReadTimeout,
		WriteTimeout:      l.WriteTimeout,
		IdleTimeout:       l.IdleTimeout,
		MaxHeaderBytes:    l.MaxHeaderBytes,
	}
}
