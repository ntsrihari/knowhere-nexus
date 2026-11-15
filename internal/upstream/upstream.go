// Package upstream is the only HTTP client Nexus uses for downstream
// services (R-09, A-17): per-integration timeouts, response size caps, a
// concurrency limit and a circuit breaker. Base URLs come only from config
// (P-12); request paths are fixed by callers, never taken from clients.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limits for every upstream call.
const (
	ConnectTimeout = 3 * time.Second
	TotalTimeout   = 10 * time.Second
	MaxResponse    = 2 << 20
	MaxConcurrent  = 4
	BreakAfter     = 5
	RetryAfter     = 30 * time.Second
)

// Errors.
var (
	ErrNotConfigured = errors.New("integration not configured")
	ErrOpen          = errors.New("circuit open: upstream recently failing")
	ErrBusy          = errors.New("upstream concurrency limit reached")
	ErrTooLarge      = errors.New("upstream response too large")
)

// StatusError is a non-2xx upstream response.
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("upstream status %d", e.Status) }

// BreakerState is reported by admin status.
type BreakerState string

const (
	Closed   BreakerState = "closed"
	Open     BreakerState = "open"
	HalfOpen BreakerState = "half_open"
)

// Client talks to one downstream service.
type Client struct {
	Name    string
	BaseURL string
	http    *http.Client
	sem     chan struct{}
	now     func() time.Time

	mu          sync.Mutex
	failures    int
	openedAt    time.Time
	lastSuccess time.Time
	lastError   string
}

// New returns a client for baseURL (no trailing slash). An empty baseURL
// gives a client whose calls return ErrNotConfigured.
func New(name, baseURL string) *Client {
	dialer := &net.Dialer{Timeout: ConnectTimeout}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   ConnectTimeout,
		ResponseHeaderTimeout: TotalTimeout,
		MaxIdleConnsPerHost:   MaxConcurrent,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{
		Name:    name,
		BaseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Transport: tr,
			Timeout:   TotalTimeout,
			// Never follow redirects to other hosts.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sem: make(chan struct{}, MaxConcurrent),
		now: time.Now,
	}
}

// Configured reports whether a base URL is set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" }

// Status describes the client for the admin status endpoint.
type Status struct {
	Name          string       `json:"name"`
	Configured    bool         `json:"configured"`
	Breaker       BreakerState `json:"breaker"`
	LastSuccessAt *time.Time   `json:"lastSuccessAt"`
	LastError     string       `json:"lastError,omitempty"`
}

// Status returns the current health of the client.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Name: c.Name, Configured: c.Configured(), Breaker: c.stateLocked(), LastError: c.lastError}
	if !c.lastSuccess.IsZero() {
		t := c.lastSuccess
		st.LastSuccessAt = &t
	}
	return st
}

func (c *Client) stateLocked() BreakerState {
	if c.failures < BreakAfter {
		return Closed
	}
	if c.now().Sub(c.openedAt) >= RetryAfter {
		return HalfOpen
	}
	return Open
}

// Request describes one call. Path must be a fixed, server-chosen path.
type Request struct {
	Method  string
	Path    string
	Query   map[string]string
	Header  http.Header
	Body    any // JSON-encoded when non-nil
	RawBody []byte
}

// Do performs the request and decodes a JSON response into out (if non-nil).
func (c *Client) Do(ctx context.Context, r Request, out any) error {
	b, err := c.DoRaw(ctx, r)
	if err != nil {
		return err
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: invalid JSON response: %w", c.Name, err)
	}
	return nil
}

// DoRaw performs the request and returns the (size-capped) body.
func (c *Client) DoRaw(ctx context.Context, r Request) ([]byte, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if !strings.HasPrefix(r.Path, "/") {
		return nil, errors.New("upstream: path must be absolute")
	}
	c.mu.Lock()
	state := c.stateLocked()
	c.mu.Unlock()
	if state == Open {
		return nil, ErrOpen
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	default:
		return nil, ErrBusy
	}

	var body io.Reader
	switch {
	case r.Body != nil:
		b, err := json.Marshal(r.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	case r.RawBody != nil:
		body = bytes.NewReader(r.RawBody)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+r.Path, body)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range r.Query {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.record(err)
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponse+1))
	if err != nil {
		c.record(err)
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	if len(data) > MaxResponse {
		c.record(ErrTooLarge)
		return nil, ErrTooLarge
	}
	if resp.StatusCode >= 500 {
		e := &StatusError{Status: resp.StatusCode}
		c.record(e)
		return nil, e
	}
	// 4xx are caller-level outcomes (bad credentials, not found), not outages.
	c.record(nil)
	if resp.StatusCode >= 300 {
		return data, &StatusError{Status: resp.StatusCode}
	}
	return data, nil
}

func (c *Client) record(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.failures = 0
		c.lastSuccess = c.now()
		c.lastError = ""
		return
	}
	c.failures++
	c.lastError = err.Error()
	if c.failures >= BreakAfter {
		c.openedAt = c.now()
	}
}

// IsStatus reports whether err is an upstream response with the given status.
func IsStatus(err error, status int) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == status
}
