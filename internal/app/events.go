package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

// hub fans out server-sent events (P-04). Each subscriber belongs to one
// session family; revoking the family closes its streams (R-16).
type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	family string
	admin  bool
	ch     chan sse
	closed chan struct{}
	once   sync.Once
}

type sse struct {
	name string
	data []byte
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

func (h *hub) add(s *subscriber) {
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
	s.once.Do(func() { close(s.closed) })
}

// publish sends an event to every subscriber allowed to see it.
func (h *hub) publish(name string, v any, adminOnly bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if adminOnly && !s.admin {
			continue
		}
		select {
		case s.ch <- sse{name: name, data: b}:
		default: // slow client: drop rather than block other widgets
		}
	}
}

// revoke notifies and closes every stream of a session family.
func (h *hub) revoke(family string) {
	h.mu.Lock()
	var hit []*subscriber
	for s := range h.subs {
		if s.family == family {
			hit = append(hit, s)
		}
	}
	h.mu.Unlock()
	for _, s := range hit {
		select {
		case s.ch <- sse{name: "session.revoked", data: []byte(`{}`)}:
		default:
		}
		s.once.Do(func() { close(s.closed) })
	}
}

// count returns the number of open streams (tests, status).
func (h *hub) count() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.subs) }

// maxStreamsPerFamily bounds long-lived connections per session.
const maxStreamsPerFamily = 4

func (h *hub) familyCount(family string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for s := range h.subs {
		if s.family == family {
			n++
		}
	}
	return n
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	if a.events.familyCount(s.FamilyID) >= maxStreamsPerFamily {
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many open event streams")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // streaming: not bound by the server WriteTimeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sub := &subscriber{family: s.FamilyID, admin: s.IsAdmin(a.now()), ch: make(chan sse, 32), closed: make(chan struct{})}
	a.events.add(sub)
	defer a.events.remove(sub)
	for _, ev := range a.widgets.snapshot(sub.admin) {
		writeEvent(w, ev)
	}
	_ = rc.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	// Re-check the session periodically: expiry ends the stream.
	recheck := time.NewTicker(time.Minute)
	defer recheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.closed:
			// Drain a final revocation notice, if queued.
			for {
				select {
				case ev := <-sub.ch:
					writeEvent(w, ev)
				default:
					_ = rc.Flush()
					return
				}
			}
		case ev := <-sub.ch:
			writeEvent(w, ev)
			_ = rc.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			_ = rc.Flush()
		case <-recheck.C:
			if _, err := a.sessions.Get(r.Context(), s.FamilyID); err != nil {
				writeEvent(w, sse{name: "session.revoked", data: []byte(`{}`)})
				_ = rc.Flush()
				return
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, ev sse) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
}
