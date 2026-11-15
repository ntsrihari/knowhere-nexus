package app

import (
	"context"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/docker"
	"github.com/ntsrihari/knowhere-nexus/internal/hostmetrics"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

// Widget states (API_SPEC §0, A-17).
const (
	stOK            = "ok"
	stStale         = "stale"
	stUnavailable   = "unavailable"
	stNotConfigured = "not_configured"
	cacheMaxAge     = 10 * time.Minute
)

// widget is one cached value with separate user and admin views (A-07, A-08).
type widget struct {
	name       string
	interval   time.Duration
	configured func() bool
	poll       func(ctx context.Context) (public, admin any, err error)

	mu      sync.Mutex
	public  any
	admin   any
	updated time.Time
	lastErr error
}

type envelope struct {
	Status    string     `json:"status"`
	UpdatedAt *time.Time `json:"updatedAt"`
	Data      any        `json:"data"`
}

func (w *widget) view(now time.Time, admin bool) envelope {
	if w.configured != nil && !w.configured() {
		return envelope{Status: stNotConfigured}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.updated.IsZero() || now.Sub(w.updated) > cacheMaxAge {
		return envelope{Status: stUnavailable}
	}
	st := stOK
	if now.Sub(w.updated) > 2*w.interval || w.lastErr != nil {
		st = stStale
	}
	u := w.updated
	data := w.public
	if admin {
		data = w.admin
	}
	return envelope{Status: st, UpdatedAt: &u, Data: data}
}

// refresh polls once, isolating panics so one widget never affects another.
func (w *widget) refresh(ctx context.Context, now func() time.Time) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = errors.New("widget poller panic")
		}
		w.mu.Lock()
		w.lastErr = err
		w.mu.Unlock()
	}()
	if w.configured != nil && !w.configured() {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, upstream.TotalTimeout+5*time.Second)
	defer cancel()
	pub, adm, err := w.poll(pctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.public, w.admin, w.updated = pub, adm, now()
	w.mu.Unlock()
	return nil
}

type widgetSet struct {
	a       *App
	host    *hostmetrics.Reader
	docker  *docker.Client
	list    []*widget
	byName  map[string]*widget
	hostMu  sync.Mutex
	trigger map[string]chan struct{}
}

func newWidgetSet(a *App) *widgetSet {
	ws := &widgetSet{a: a, host: hostmetrics.New(), byName: map[string]*widget{}, trigger: map[string]chan struct{}{},
		docker: docker.New(upstream.New("docker", a.cfg.DockerURL))}
	ws.add(&widget{name: "system", interval: 5 * time.Second, poll: ws.pollSystem})
	ws.add(&widget{name: "storage", interval: 30 * time.Second, poll: ws.pollStorage,
		configured: func() bool { return len(a.cfg.Mounts) > 0 }})
	ws.add(&widget{name: "services", interval: 15 * time.Second, poll: ws.pollServices,
		configured: ws.docker.Upstream().Configured})
	for _, w := range a.integrationWidgets() {
		ws.add(w)
	}
	return ws
}

func (ws *widgetSet) add(w *widget) {
	ws.list = append(ws.list, w)
	ws.byName[w.name] = w
	ws.trigger[w.name] = make(chan struct{}, 1)
}

func (ws *widgetSet) start(ctx context.Context) {
	for _, w := range ws.list {
		go ws.loop(ctx, w)
	}
}

func (ws *widgetSet) loop(ctx context.Context, w *widget) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		if err := w.refresh(ctx, ws.a.now); err != nil && !errors.Is(err, context.Canceled) {
			ws.a.log.Debug("widget poll failed", "widget", w.name, "error", err.Error())
		}
		now := ws.a.now()
		ws.a.events.publish("widget."+w.name, w.view(now, false), false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ws.trigger[w.name]:
		}
	}
}

// poke asks a widget to refresh now (admin re-check).
func (ws *widgetSet) poke(name string) {
	select {
	case ws.trigger[name] <- struct{}{}:
	default:
	}
}

// snapshot returns the current events for a new SSE subscriber.
func (ws *widgetSet) snapshot(admin bool) []sse {
	now := ws.a.now()
	var out []sse
	for _, w := range ws.list {
		if b, err := jsonBytes(w.view(now, admin)); err == nil {
			out = append(out, sse{name: "widget." + w.name, data: b})
		}
	}
	return out
}

func (ws *widgetSet) handler(name string, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wg := ws.byName[name]
		if wg == nil {
			httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "unknown widget")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, wg.view(ws.a.now(), admin))
	}
}

// --- system -----------------------------------------------------------------

func (ws *widgetSet) pollSystem(context.Context) (any, any, error) {
	ws.hostMu.Lock()
	defer ws.hostMu.Unlock()
	s, err := ws.host.Read()
	if err != nil {
		return nil, nil, err
	}
	s.CPUPercent = math.Round(s.CPUPercent*10) / 10
	return s, s, nil
}

// --- storage ----------------------------------------------------------------

type storagePublic struct {
	Label       string   `json:"label"`
	Health      string   `json:"health"`
	UsedPercent *float64 `json:"usedPercent"`
}

type storageAdmin struct {
	Label    string  `json:"label"`
	Path     string  `json:"path"`
	Mounted  bool    `json:"mounted"`
	Mode     *string `json:"mode"`
	Free     *uint64 `json:"free"`
	Total    *uint64 `json:"total"`
	Excluded bool    `json:"excluded"`
	Smart    string  `json:"smart"`
}

func (a *App) isExcluded(p string) bool {
	for _, e := range a.cfg.Excluded {
		if p == e || strings.HasPrefix(p, e+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (ws *widgetSet) pollStorage(context.Context) (any, any, error) {
	ws.hostMu.Lock()
	info, err := ws.host.Mountinfo()
	ws.hostMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	pub := []storagePublic{}
	adm := []storageAdmin{}
	for _, m := range ws.a.cfg.Mounts {
		if ws.a.isExcluded(m.HostPath) || ws.a.isExcluded(m.Path) {
			continue // never shown as usable (S-10)
		}
		st := ws.host.Inspect(m.Path, info)
		p := storagePublic{Label: m.Label, Health: "unavailable"}
		ad := storageAdmin{Label: m.Label, Path: m.HostPath, Mounted: st.Mounted, Free: st.Free, Total: st.Total, Smart: "unavailable"}
		if st.Mounted {
			mode := "rw"
			if st.ReadOnly {
				mode = "ro"
			}
			ad.Mode = &mode
			p.Health = "ok"
			if st.Total != nil && *st.Total > 0 {
				used := math.Round(float64(*st.Total-*st.Free)/float64(*st.Total)*1000) / 10
				p.UsedPercent = &used
				if used >= 90 {
					p.Health = "degraded"
				}
			}
		}
		pub = append(pub, p)
		adm = append(adm, ad)
	}
	for _, e := range ws.a.cfg.Excluded {
		adm = append(adm, storageAdmin{Label: filepath.Base(e), Path: e, Excluded: true, Smart: "unavailable"})
	}
	return map[string]any{"mounts": pub}, map[string]any{"mounts": adm}, nil
}

// --- services ---------------------------------------------------------------

type serviceApp struct {
	CatalogID string `json:"catalogId"`
	Name      string `json:"name"`
	Up        bool   `json:"up"`
	audience  string
}

func (ws *widgetSet) pollServices(ctx context.Context) (any, any, error) {
	list, err := ws.docker.List(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	state := map[string]string{}
	for _, c := range list {
		state[c.Name] = c.State
	}
	var apps []serviceApp
	for _, app := range ws.a.catalog.Get().Apps {
		if app.Kind != "server" || app.Health == nil || len(app.Health.Containers) == 0 {
			continue
		}
		up := true
		for _, c := range app.Health.Containers {
			if state[c] != "running" {
				up = false
			}
		}
		apps = append(apps, serviceApp{CatalogID: app.ID, Name: app.Name, Up: up, audience: app.Audience})
	}
	return servicesView{apps: apps}, map[string]any{"containers": list, "apps": apps}, nil
}

// servicesView hides admin-audience apps from users when serialised.
type servicesView struct{ apps []serviceApp }

func (v servicesView) MarshalJSON() ([]byte, error) {
	pub := []serviceApp{}
	for _, a := range v.apps {
		if a.audience != "admin" {
			pub = append(pub, a)
		}
	}
	return jsonBytes(map[string]any{"apps": pub})
}
