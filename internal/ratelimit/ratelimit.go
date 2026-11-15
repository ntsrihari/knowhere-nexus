// Package ratelimit counts authentication failures per key in a sliding window.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most Max failures per key within Window.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time
	mu     sync.Mutex
	hits   map[string][]time.Time
}

// New returns a limiter.
func New(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, Now: time.Now, hits: map[string][]time.Time{}}
}

func (l *Limiter) recent(key string) []time.Time {
	cut := l.Now().Add(-l.Window)
	h := l.hits[key]
	i := 0
	for i < len(h) && !h[i].After(cut) {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// Allowed reports whether key is under its limit.
func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) < l.Max
}

// Fail records a failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.recent(key), l.Now())
	if len(l.hits) > 100000 { // bound memory under abuse
		l.hits = map[string][]time.Time{}
	}
}

// Reset clears key (after a success).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
