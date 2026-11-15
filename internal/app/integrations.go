package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/authz"
	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
	"github.com/ntsrihari/knowhere-nexus/internal/secrets"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

// integrations holds one upstream client per configured service plus the
// Jellyfin admin broker (R-10). API keys never leave Nexus (S-05).
type integrations struct {
	clients map[string]*upstream.Client
	keys    map[string]secrets.Secret
	jfAdmin *upstream.Client
	jfKey   secrets.Secret

	mu        sync.Mutex
	piholeSID string
	qbitSID   string
	lastPlay  map[string]bool // session keys seen, for "stream started"
	lastDone  map[string]bool // torrents seen complete
	notified  map[string]bool // widget outage notifications
}

func (a *App) ints() *integrations {
	a.intOnce.Do(func() {
		in := &integrations{clients: map[string]*upstream.Client{}, keys: map[string]secrets.Secret{},
			lastPlay: map[string]bool{}, lastDone: map[string]bool{}, notified: map[string]bool{}}
		for _, name := range []string{"immich", "pihole", "qbittorrent", "radarr", "sonarr", "prowlarr", "jellyseerr"} {
			in.clients[name] = upstream.New(name, a.cfg.Integrations[name])
			if k, ok := a.secrets.Integrations[name]; ok {
				in.keys[name] = k
			}
		}
		in.jfKey = a.secrets.JellyfinAdminKey
		jfURL := ""
		if !in.jfKey.IsZero() {
			jfURL = a.cfg.JellyfinURL
		}
		in.jfAdmin = upstream.New("jellyfin-admin", jfURL)
		a.integ = in
	})
	return a.integ
}

func (a *App) integrationClients() []*upstream.Client {
	in := a.ints()
	out := []*upstream.Client{in.jfAdmin}
	names := make([]string, 0, len(in.clients))
	for n := range in.clients {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, in.clients[n])
	}
	return out
}

func (in *integrations) configured(name string) bool {
	c := in.clients[name]
	_, key := in.keys[name]
	return c.Configured() && key
}

func (in *integrations) apiKey(name string) http.Header {
	k := in.keys[name]
	return http.Header{"X-Api-Key": {string(k.Expose())}}
}

func (in *integrations) jfHeader() http.Header {
	return http.Header{"Authorization": {`MediaBrowser Client="Knowhere Nexus", Device="Nexus", DeviceId="nexus-admin-broker", Version="1", Token="` +
		strings.NewReplacer(`"`, "").Replace(string(in.jfKey.Expose())) + `"`}}
}

// --- widgets ----------------------------------------------------------------

func (a *App) integrationWidgetList() []*widget {
	in := a.ints()
	return []*widget{
		{name: "now-playing", interval: 15 * time.Second, poll: a.pollNowPlaying, configured: in.jfAdmin.Configured},
		{name: "photos", interval: 5 * time.Minute, poll: a.pollPhotos, configured: func() bool { return in.configured("immich") }},
		{name: "dns", interval: time.Minute, poll: a.pollDNS, configured: func() bool { return in.configured("pihole") }},
		{name: "downloads", interval: 10 * time.Second, poll: a.pollDownloads, configured: func() bool { return in.configured("qbittorrent") }},
		{name: "upcoming", interval: 15 * time.Minute, poll: a.pollUpcoming,
			configured: func() bool { return in.configured("radarr") || in.configured("sonarr") }},
		{name: "queue", interval: 30 * time.Second, poll: a.pollQueue,
			configured: func() bool { return in.configured("radarr") || in.configured("sonarr") }},
		{name: "indexers", interval: 5 * time.Minute, poll: a.pollIndexers, configured: func() bool { return in.configured("prowlarr") }},
	}
}

type stream struct {
	User             string   `json:"user"`
	Title            string   `json:"title"`
	Client           string   `json:"client"`
	Device           string   `json:"device"`
	PlayMethod       string   `json:"playMethod"`
	TranscodeReasons []string `json:"transcodeReasons"`
	Progress         float64  `json:"progress"`
	Paused           bool     `json:"paused"`
	key              string
	itemType         string
}

func (a *App) pollNowPlaying(ctx context.Context) (any, any, error) {
	in := a.ints()
	var raw []struct {
		ID             string `json:"Id"`
		UserName       string `json:"UserName"`
		Client         string `json:"Client"`
		DeviceName     string `json:"DeviceName"`
		NowPlayingItem *struct {
			Name         string `json:"Name"`
			SeriesName   string `json:"SeriesName"`
			Type         string `json:"Type"`
			RunTimeTicks int64  `json:"RunTimeTicks"`
			ParentIndex  int    `json:"ParentIndexNumber"`
			IndexNumber  int    `json:"IndexNumber"`
		} `json:"NowPlayingItem"`
		PlayState struct {
			PositionTicks int64  `json:"PositionTicks"`
			IsPaused      bool   `json:"IsPaused"`
			PlayMethod    string `json:"PlayMethod"`
		} `json:"PlayState"`
		TranscodingInfo *struct {
			TranscodeReasons []string `json:"TranscodeReasons"`
		} `json:"TranscodingInfo"`
	}
	if err := in.jfAdmin.Do(ctx, upstream.Request{Path: "/Sessions", Query: map[string]string{"activeWithinSeconds": "960"},
		Header: in.jfHeader()}, &raw); err != nil {
		return nil, nil, err
	}
	streams := []stream{}
	for _, s := range raw {
		it := s.NowPlayingItem
		if it == nil {
			continue
		}
		title := it.Name
		if it.SeriesName != "" {
			title = fmt.Sprintf("%s · S%02dE%02d · %s", it.SeriesName, it.ParentIndex, it.IndexNumber, it.Name)
		}
		st := stream{User: s.UserName, Title: title, Client: s.Client, Device: s.DeviceName, Paused: s.PlayState.IsPaused,
			PlayMethod:       map[string]string{"DirectPlay": "direct_play", "DirectStream": "direct_stream", "Transcode": "transcode"}[s.PlayState.PlayMethod],
			TranscodeReasons: []string{}, key: s.ID + "|" + title, itemType: it.Type}
		if st.PlayMethod == "" {
			st.PlayMethod = "direct_play"
		}
		if s.TranscodingInfo != nil && s.TranscodingInfo.TranscodeReasons != nil {
			st.TranscodeReasons = s.TranscodingInfo.TranscodeReasons
		}
		if it.RunTimeTicks > 0 {
			st.Progress = float64(int(float64(s.PlayState.PositionTicks)/float64(it.RunTimeTicks)*1000)) / 10
		}
		streams = append(streams, st)
	}
	a.recordWatch(ctx, streams)
	v := map[string]any{"streams": streams}
	return v, v, nil
}

// recordWatch stores one sample per active stream for analytics and emits
// "stream started" notifications.
func (a *App) recordWatch(ctx context.Context, streams []stream) {
	in := a.ints()
	now := a.now()
	seen := map[string]bool{}
	for _, s := range streams {
		seen[s.key] = true
		if !s.Paused {
			tc := 0
			if s.PlayMethod == "transcode" {
				tc = 1
			}
			_, _ = a.db.ExecContext(ctx, `INSERT INTO watch_samples (at, user_name, item, item_type, client, transcode) VALUES (?,?,?,?,?,?)`,
				now.UnixMilli(), s.User, s.Title, s.itemType, s.Client, tc)
		}
		in.mu.Lock()
		isNew := !in.lastPlay[s.key]
		in.mu.Unlock()
		if isNew {
			a.notify(ctx, "stream.started", s.User+" started watching", s.Title, true)
		}
	}
	in.mu.Lock()
	in.lastPlay = seen
	in.mu.Unlock()
}

func (a *App) pollPhotos(ctx context.Context) (any, any, error) {
	in := a.ints()
	var st struct {
		Photos int64 `json:"photos"`
		Videos int64 `json:"videos"`
		Usage  int64 `json:"usage"`
	}
	h := http.Header{"X-Api-Key": in.apiKey("immich")["X-Api-Key"]}
	if err := in.clients["immich"].Do(ctx, upstream.Request{Path: "/api/server/statistics", Header: h}, &st); err != nil {
		return nil, nil, err
	}
	v := map[string]any{"assets": st.Photos + st.Videos, "photos": st.Photos, "videos": st.Videos, "libraryBytes": st.Usage}
	return v, v, nil
}

func (a *App) pollDNS(ctx context.Context) (any, any, error) {
	in := a.ints()
	c := in.clients["pihole"]
	var sum struct {
		Queries struct {
			Total          int64   `json:"total"`
			Blocked        int64   `json:"blocked"`
			PercentBlocked float64 `json:"percent_blocked"`
		} `json:"queries"`
	}
	for attempt := 0; attempt < 2; attempt++ {
		in.mu.Lock()
		sid := in.piholeSID
		in.mu.Unlock()
		if sid == "" {
			var auth struct {
				Session struct {
					SID   string `json:"sid"`
					Valid bool   `json:"valid"`
				} `json:"session"`
			}
			if err := c.Do(ctx, upstream.Request{Method: http.MethodPost, Path: "/api/auth",
				Body: map[string]string{"password": string(in.keys["pihole"].Expose())}}, &auth); err != nil {
				return nil, nil, err
			}
			if !auth.Session.Valid || auth.Session.SID == "" {
				return nil, nil, errors.New("pihole: authentication failed")
			}
			sid = auth.Session.SID
			in.mu.Lock()
			in.piholeSID = sid
			in.mu.Unlock()
		}
		err := c.Do(ctx, upstream.Request{Path: "/api/stats/summary", Header: http.Header{"X-FTL-SID": {sid}}}, &sum)
		if upstream.IsStatus(err, 401) {
			in.mu.Lock()
			in.piholeSID = ""
			in.mu.Unlock()
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		v := map[string]any{"queries": sum.Queries.Total, "blocked": sum.Queries.Blocked,
			"blockedPercent": float64(int(sum.Queries.PercentBlocked*10)) / 10}
		return v, v, nil
	}
	return nil, nil, errors.New("pihole: session rejected")
}

type download struct {
	Name     string  `json:"name"`
	Progress float64 `json:"progress"`
	State    string  `json:"state"`
	DLSpeed  int64   `json:"dlSpeed"`
	ULSpeed  int64   `json:"ulSpeed"`
	ETA      int64   `json:"eta"`
	Category string  `json:"category"`
	hash     string
}

func (a *App) qbitGet(ctx context.Context, path string, query map[string]string, out any) error {
	in := a.ints()
	c := in.clients["qbittorrent"]
	for attempt := 0; attempt < 2; attempt++ {
		in.mu.Lock()
		sid := in.qbitSID
		in.mu.Unlock()
		if sid == "" {
			user, pass, _ := strings.Cut(string(in.keys["qbittorrent"].Expose()), ":")
			var hdr http.Header
			b, err := c.DoRaw(ctx, upstream.Request{Method: http.MethodPost, Path: "/api/v2/auth/login",
				Form: map[string]string{"username": user, "password": pass}, RespHeader: &hdr,
				Header: http.Header{"Referer": {c.BaseURL}}})
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(b)) != "Ok." {
				return errors.New("qbittorrent: authentication failed")
			}
			for _, ck := range (&http.Response{Header: hdr}).Cookies() {
				if ck.Name == "SID" {
					sid = ck.Value
				}
			}
			if sid == "" {
				return errors.New("qbittorrent: no session cookie")
			}
			in.mu.Lock()
			in.qbitSID = sid
			in.mu.Unlock()
		}
		err := c.Do(ctx, upstream.Request{Path: path, Query: query, Header: http.Header{"Cookie": {"SID=" + sid}}}, out)
		if upstream.IsStatus(err, 403) || upstream.IsStatus(err, 401) {
			in.mu.Lock()
			in.qbitSID = ""
			in.mu.Unlock()
			continue
		}
		return err
	}
	return errors.New("qbittorrent: session rejected")
}

func (a *App) pollDownloads(ctx context.Context) (any, any, error) {
	var raw []struct {
		Hash     string  `json:"hash"`
		Name     string  `json:"name"`
		Progress float64 `json:"progress"`
		State    string  `json:"state"`
		DLSpeed  int64   `json:"dlspeed"`
		ULSpeed  int64   `json:"upspeed"`
		ETA      int64   `json:"eta"`
		Category string  `json:"category"`
	}
	if err := a.qbitGet(ctx, "/api/v2/torrents/info", nil, &raw); err != nil {
		return nil, nil, err
	}
	in := a.ints()
	items := []download{}
	for _, t := range raw {
		d := download{Name: t.Name, Progress: float64(int(t.Progress*1000)) / 10, State: t.State, DLSpeed: t.DLSpeed,
			ULSpeed: t.ULSpeed, ETA: t.ETA, Category: t.Category, hash: t.Hash}
		items = append(items, d)
		in.mu.Lock()
		first, seen := in.lastDone[t.Hash]
		complete := t.Progress >= 1
		in.lastDone[t.Hash] = complete
		in.mu.Unlock()
		if complete && seen && !first {
			a.notify(ctx, "download.finished", "Download finished", t.Name, false)
		}
	}
	v := map[string]any{"items": items}
	return v, v, nil
}

type upcomingItem struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Date   string `json:"date"`
	Source string `json:"source"`
}

func (a *App) calendar(ctx context.Context, from, to time.Time) ([]upcomingItem, error) {
	in := a.ints()
	q := map[string]string{"start": from.Format("2006-01-02"), "end": to.Format("2006-01-02")}
	items := []upcomingItem{}
	var errs []error
	if in.configured("radarr") {
		var movies []struct {
			Title           string `json:"title"`
			InCinemas       string `json:"inCinemas"`
			DigitalRelease  string `json:"digitalRelease"`
			PhysicalRelease string `json:"physicalRelease"`
		}
		if err := in.clients["radarr"].Do(ctx, upstream.Request{Path: "/api/v3/calendar", Query: q, Header: in.apiKey("radarr")}, &movies); err != nil {
			errs = append(errs, err)
		}
		for _, m := range movies {
			d := m.DigitalRelease
			if d == "" {
				d = m.PhysicalRelease
			}
			if d == "" {
				d = m.InCinemas
			}
			items = append(items, upcomingItem{Title: m.Title, Kind: "movie", Date: d, Source: "radarr"})
		}
	}
	if in.configured("sonarr") {
		q2 := map[string]string{"start": q["start"], "end": q["end"], "includeSeries": "true"}
		var eps []struct {
			Title      string `json:"title"`
			AirDateUtc string `json:"airDateUtc"`
			Season     int    `json:"seasonNumber"`
			Episode    int    `json:"episodeNumber"`
			Series     struct {
				Title string `json:"title"`
			} `json:"series"`
		}
		if err := in.clients["sonarr"].Do(ctx, upstream.Request{Path: "/api/v3/calendar", Query: q2, Header: in.apiKey("sonarr")}, &eps); err != nil {
			errs = append(errs, err)
		}
		for _, e := range eps {
			items = append(items, upcomingItem{Title: fmt.Sprintf("%s · S%02dE%02d · %s", e.Series.Title, e.Season, e.Episode, e.Title),
				Kind: "episode", Date: e.AirDateUtc, Source: "sonarr"})
		}
	}
	if len(items) == 0 && len(errs) > 0 {
		return nil, errs[0]
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Date < items[j].Date })
	return items, nil
}

func (a *App) pollUpcoming(ctx context.Context) (any, any, error) {
	now := a.now()
	items, err := a.calendar(ctx, now, now.AddDate(0, 0, 14))
	if err != nil {
		return nil, nil, err
	}
	v := map[string]any{"items": items}
	return v, v, nil
}

type queueItem struct {
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	TimeLeft string  `json:"timeLeft"`
	Source   string  `json:"source"`
}

func (a *App) pollQueue(ctx context.Context) (any, any, error) {
	in := a.ints()
	items := []queueItem{}
	var firstErr error
	for _, src := range []string{"radarr", "sonarr"} {
		if !in.configured(src) {
			continue
		}
		var q struct {
			Records []struct {
				Title    string  `json:"title"`
				Status   string  `json:"status"`
				Size     float64 `json:"size"`
				SizeLeft float64 `json:"sizeleft"`
				TimeLeft string  `json:"timeleft"`
			} `json:"records"`
		}
		if err := in.clients[src].Do(ctx, upstream.Request{Path: "/api/v3/queue", Query: map[string]string{"pageSize": "50"},
			Header: in.apiKey(src)}, &q); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range q.Records {
			p := 0.0
			if r.Size > 0 {
				p = float64(int((r.Size-r.SizeLeft)/r.Size*1000)) / 10
			}
			items = append(items, queueItem{Title: r.Title, Status: r.Status, Progress: p, TimeLeft: r.TimeLeft, Source: src})
		}
	}
	if len(items) == 0 && firstErr != nil {
		return nil, nil, firstErr
	}
	v := map[string]any{"items": items}
	return v, v, nil
}

func (a *App) pollIndexers(ctx context.Context) (any, any, error) {
	in := a.ints()
	var idx []struct {
		Name   string `json:"name"`
		Enable bool   `json:"enable"`
	}
	if err := in.clients["prowlarr"].Do(ctx, upstream.Request{Path: "/api/v1/indexer", Header: in.apiKey("prowlarr")}, &idx); err != nil {
		return nil, nil, err
	}
	var status []struct {
		IndexerID    int    `json:"indexerId"`
		DisabledTill string `json:"disabledTill"`
	}
	_ = in.clients["prowlarr"].Do(ctx, upstream.Request{Path: "/api/v1/indexerstatus", Header: in.apiKey("prowlarr")}, &status)
	enabled := 0
	names := []string{}
	for _, i := range idx {
		if i.Enable {
			enabled++
		}
		names = append(names, i.Name)
	}
	v := map[string]any{"total": len(idx), "enabled": enabled, "failing": len(status), "names": names}
	return v, v, nil
}

// --- notifications ----------------------------------------------------------

func (a *App) notify(ctx context.Context, kind, title, body string, adminOnly bool) {
	ao := 0
	if adminOnly {
		ao = 1
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO notifications (at, kind, title, body, admin_only) VALUES (?,?,?,?,?)`,
		a.now().UnixMilli(), kind, title, body, ao)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM notifications WHERE seq <= (SELECT MAX(seq) - 500 FROM notifications)`)
	a.events.publish("notification", map[string]string{"kind": kind, "title": title, "body": body}, adminOnly)
}

// watchOutages emits server-down notifications when a widget goes
// unavailable and back.
func (a *App) watchOutages(ctx context.Context) {
	in := a.ints()
	for _, w := range a.widgets.list {
		st := w.view(a.now(), true).Status
		in.mu.Lock()
		was := in.notified[w.name]
		down := st == stUnavailable && w.configured != nil && w.configured()
		in.notified[w.name] = down
		in.mu.Unlock()
		if down && !was {
			a.notify(ctx, "service.down", w.name+" is unavailable", "Nexus cannot reach this service.", true)
		} else if !down && was {
			a.notify(ctx, "service.up", w.name+" is back", "", true)
		}
	}
}

func (a *App) handleNotifications(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	q := `SELECT seq, at, kind, title, body FROM notifications WHERE seq > ?`
	if !s.IsAdmin(a.now()) {
		q += ` AND admin_only = 0`
	}
	rows, err := a.db.QueryContext(r.Context(), q+` ORDER BY seq DESC LIMIT 100`, since)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "notifications unavailable")
		return
	}
	defer rows.Close()
	type note struct {
		Seq   int64     `json:"seq"`
		At    time.Time `json:"at"`
		Kind  string    `json:"kind"`
		Title string    `json:"title"`
		Body  string    `json:"body"`
	}
	out := []note{}
	for rows.Next() {
		var n note
		var at int64
		if rows.Scan(&n.Seq, &at, &n.Kind, &n.Title, &n.Body) == nil {
			n.At = time.UnixMilli(at).UTC()
			out = append(out, n)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// --- Jellyfin admin operations ----------------------------------------------

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	in := a.ints()
	var raw []struct {
		ID                  string `json:"Id"`
		Name                string `json:"Name"`
		Category            string `json:"Category"`
		State               string `json:"State"`
		LastExecutionResult *struct {
			Status  string `json:"Status"`
			EndTime string `json:"EndTimeUtc"`
		} `json:"LastExecutionResult"`
	}
	if err := in.jfAdmin.Do(r.Context(), upstream.Request{Path: "/ScheduledTasks", Header: in.jfHeader()}, &raw); err != nil {
		writeUpstream(w, err)
		return
	}
	out := []map[string]any{}
	for _, t := range raw {
		m := map[string]any{"id": t.ID, "name": t.Name, "category": t.Category, "state": t.State, "lastResult": nil, "lastRunAt": nil}
		if t.LastExecutionResult != nil {
			m["lastResult"], m["lastRunAt"] = t.LastExecutionResult.Status, t.LastExecutionResult.EndTime
		}
		out = append(out, m)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) handleRunTask(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	in := a.ints()
	id := r.PathValue("id")
	var raw []struct {
		ID string `json:"Id"`
	}
	if err := in.jfAdmin.Do(r.Context(), upstream.Request{Path: "/ScheduledTasks", Header: in.jfHeader()}, &raw); err != nil {
		writeUpstream(w, err)
		return
	}
	found := false
	for _, t := range raw {
		found = found || t.ID == id
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "unknown task")
		return
	}
	if !a.writeAudit(w, r, a.entry(r, &s, "jellyfin.task.run", id, "ok", nil)) {
		return
	}
	if _, err := in.jfAdmin.DoRaw(r.Context(), upstream.Request{Method: http.MethodPost, Path: "/ScheduledTasks/Running/" + url.PathEscape(id),
		Header: in.jfHeader()}); err != nil {
		writeUpstream(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeUpstream(w http.ResponseWriter, err error) {
	if errors.Is(err, upstream.ErrNotConfigured) {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotConfigured, "integration not configured")
		return
	}
	httpx.WriteError(w, http.StatusBadGateway, httpx.CodeUpstream, "upstream service unavailable")
}

// --- analytics (watch statistics) -------------------------------------------

func (a *App) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	since := a.now().AddDate(0, 0, -days).UnixMilli()
	const sampleMinutes = 0.25 // one sample per 15 s poll
	query := func(q string) [][2]any {
		rows, err := a.db.QueryContext(r.Context(), q, since)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out [][2]any
		for rows.Next() {
			var k string
			var n int64
			if rows.Scan(&k, &n) == nil {
				out = append(out, [2]any{k, n})
			}
		}
		return out
	}
	conv := func(rows [][2]any, key string) []map[string]any {
		out := []map[string]any{}
		for _, r := range rows {
			out = append(out, map[string]any{key: r[0], "minutes": float64(r[1].(int64)) * sampleMinutes})
		}
		return out
	}
	byUser := conv(query(`SELECT user_name, COUNT(*) FROM watch_samples WHERE at >= ? GROUP BY user_name ORDER BY 2 DESC`), "user")
	top := conv(query(`SELECT item, COUNT(*) FROM watch_samples WHERE at >= ? GROUP BY item ORDER BY 2 DESC LIMIT 10`), "title")
	hours := make([]float64, 24)
	for _, r := range query(`SELECT CAST(strftime('%H', at / 1000, 'unixepoch', 'localtime') AS TEXT), COUNT(*) FROM watch_samples WHERE at >= ? GROUP BY 1`) {
		h, _ := strconv.Atoi(r[0].(string))
		if h >= 0 && h < 24 {
			hours[h] = float64(r[1].(int64)) * sampleMinutes
		}
	}
	var transcodes, total int64
	_ = a.db.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(transcode),0), COUNT(*) FROM watch_samples WHERE at >= ?`, since).Scan(&transcodes, &total)
	tp := 0.0
	if total > 0 {
		tp = float64(int(float64(transcodes)/float64(total)*1000)) / 10
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"days": days, "byUser": byUser, "topTitles": top,
		"minutesByHour": hours, "transcodePercent": tp, "totalMinutes": float64(total) * sampleMinutes})
}

// --- requests (Jellyseerr, D-11) --------------------------------------------

// seerrUser maps the Nexus user to a Jellyseerr user id by Jellyfin id, so
// requests are attributed to the person making them.
func (a *App) seerrUser(ctx context.Context, jellyfinID string) (string, error) {
	in := a.ints()
	var res struct {
		Results []struct {
			ID             int    `json:"id"`
			JellyfinUserID string `json:"jellyfinUserId"`
		} `json:"results"`
	}
	if err := in.clients["jellyseerr"].Do(ctx, upstream.Request{Path: "/api/v1/user", Query: map[string]string{"take": "500"},
		Header: in.apiKey("jellyseerr")}, &res); err != nil {
		return "", err
	}
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	for _, u := range res.Results {
		if norm(u.JellyfinUserID) == norm(jellyfinID) {
			return strconv.Itoa(u.ID), nil
		}
	}
	return "", errors.New("no Jellyseerr account linked to this Jellyfin user")
}

func (a *App) handleRequestSearch(w http.ResponseWriter, r *http.Request) {
	in := a.ints()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len([]rune(q)) > 100 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "q must be 1-100 characters")
		return
	}
	var res struct {
		Results []struct {
			ID          int    `json:"id"`
			MediaType   string `json:"mediaType"`
			Title       string `json:"title"`
			Name        string `json:"name"`
			ReleaseDate string `json:"releaseDate"`
			FirstAir    string `json:"firstAirDate"`
			Overview    string `json:"overview"`
			MediaInfo   *struct {
				Status int `json:"status"`
			} `json:"mediaInfo"`
		} `json:"results"`
	}
	if err := in.clients["jellyseerr"].Do(r.Context(), upstream.Request{Path: "/api/v1/search",
		Query: map[string]string{"query": q, "page": "1"}, Header: in.apiKey("jellyseerr")}, &res); err != nil {
		writeUpstream(w, err)
		return
	}
	out := []map[string]any{}
	for _, it := range res.Results {
		if it.MediaType != "movie" && it.MediaType != "tv" {
			continue
		}
		title, date := it.Title, it.ReleaseDate
		if it.MediaType == "tv" {
			title, date = it.Name, it.FirstAir
		}
		status := 0
		if it.MediaInfo != nil {
			status = it.MediaInfo.Status
		}
		out = append(out, map[string]any{"tmdbId": it.ID, "mediaType": it.MediaType, "title": title, "year": strings.SplitN(date, "-", 2)[0],
			"overview": it.Overview, "available": status == 5, "requested": status >= 2 && status < 5})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	in := a.ints()
	var body struct {
		MediaType string `json:"mediaType"`
		TmdbID    int    `json:"tmdbId"`
		Seasons   []int  `json:"seasons"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if (body.MediaType != "movie" && body.MediaType != "tv") || body.TmdbID <= 0 || len(body.Seasons) > 100 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "mediaType must be movie or tv with a tmdbId")
		return
	}
	uid, err := a.seerrUser(r.Context(), s.UserID)
	if err != nil {
		if errors.Is(err, upstream.ErrNotConfigured) || upstream.IsStatus(err, 500) {
			writeUpstream(w, err)
			return
		}
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, err.Error())
		return
	}
	req := map[string]any{"mediaType": body.MediaType, "mediaId": body.TmdbID}
	if body.MediaType == "tv" {
		if len(body.Seasons) == 0 {
			req["seasons"] = "all"
		} else {
			req["seasons"] = body.Seasons
		}
	}
	hdr := in.apiKey("jellyseerr")
	hdr.Set("X-Api-User", uid)
	var out map[string]any
	if err := in.clients["jellyseerr"].Do(r.Context(), upstream.Request{Method: http.MethodPost, Path: "/api/v1/request", Body: req, Header: hdr}, &out); err != nil {
		if upstream.IsStatus(err, 409) {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, "already requested")
			return
		}
		writeUpstream(w, err)
		return
	}
	_ = a.audit.Write(r.Context(), a.entry(r, &s, "request.create", fmt.Sprintf("%s:%d", body.MediaType, body.TmdbID), "ok", nil))
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"status": "requested"})
}

func (a *App) handleListRequests(w http.ResponseWriter, r *http.Request) {
	s := mustSession(r)
	in := a.ints()
	q := map[string]string{"take": "50", "sort": "added"}
	if !(r.URL.Query().Get("all") == "true" && s.IsAdmin(a.now())) {
		uid, err := a.seerrUser(r.Context(), s.UserID)
		if err != nil {
			if errors.Is(err, upstream.ErrNotConfigured) {
				writeUpstream(w, err)
				return
			}
			httpx.WriteJSON(w, http.StatusOK, []any{})
			return
		}
		q["requestedBy"] = uid
	}
	var res struct {
		Results []struct {
			ID        int    `json:"id"`
			Status    int    `json:"status"`
			CreatedAt string `json:"createdAt"`
			Type      string `json:"type"`
			Media     struct {
				TmdbID int `json:"tmdbId"`
				Status int `json:"status"`
			} `json:"media"`
			RequestedBy struct {
				DisplayName string `json:"displayName"`
			} `json:"requestedBy"`
		} `json:"results"`
	}
	if err := in.clients["jellyseerr"].Do(r.Context(), upstream.Request{Path: "/api/v1/request", Query: q, Header: in.apiKey("jellyseerr")}, &res); err != nil {
		writeUpstream(w, err)
		return
	}
	statuses := map[int]string{1: "pending", 2: "approved", 3: "declined"}
	media := map[int]string{1: "unknown", 2: "pending", 3: "processing", 4: "partially_available", 5: "available"}
	out := []map[string]any{}
	for _, it := range res.Results {
		out = append(out, map[string]any{"id": it.ID, "mediaType": it.Type, "tmdbId": it.Media.TmdbID, "createdAt": it.CreatedAt,
			"status": statuses[it.Status], "mediaStatus": media[it.Media.Status], "requestedBy": it.RequestedBy.DisplayName})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *App) handleCalendar(w http.ResponseWriter, r *http.Request) {
	parse := func(s string, def time.Time) (time.Time, bool) {
		if s == "" {
			return def, true
		}
		t, err := time.Parse("2006-01-02", s)
		return t, err == nil
	}
	now := a.now()
	from, ok1 := parse(r.URL.Query().Get("from"), now)
	to, ok2 := parse(r.URL.Query().Get("to"), now.AddDate(0, 0, 14))
	if !ok1 || !ok2 || to.Before(from) || to.Sub(from) > 60*24*time.Hour {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, "from/to must be dates at most 60 days apart")
		return
	}
	items, err := a.calendar(r.Context(), from, to)
	if err != nil {
		writeUpstream(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (a *App) registerIntegrationRoutes() {
	get, post := []string{"GET"}, []string{"POST"}
	R := a.routes.Handle
	R("/api/v1/admin/widgets/now-playing", authz.Admin, get, a.widgetHandler("now-playing", true))
	R("/api/v1/admin/widgets/photos", authz.Admin, get, a.widgetHandler("photos", true))
	R("/api/v1/admin/widgets/dns", authz.Admin, get, a.widgetHandler("dns", true))
	R("/api/v1/admin/widgets/downloads", authz.Admin, get, a.widgetHandler("downloads", true))
	R("/api/v1/admin/widgets/indexers", authz.Admin, get, a.widgetHandler("indexers", true))
	R("/api/v1/admin/arr/queue", authz.Admin, get, a.widgetHandler("queue", true))
	R("/api/v1/admin/arr/calendar", authz.Admin, get, http.HandlerFunc(a.handleCalendar))
	R("/api/v1/widgets/upcoming", authz.User, get, a.widgetHandler("upcoming", false))
	R("/api/v1/admin/jellyfin/tasks", authz.AdminFresh, get, http.HandlerFunc(a.handleTasks))
	R("/api/v1/admin/jellyfin/tasks/{id}/run", authz.AdminFresh, post, http.HandlerFunc(a.handleRunTask))
	R("/api/v1/admin/analytics", authz.Admin, get, http.HandlerFunc(a.handleAnalytics))
	R("/api/v1/requests/search", authz.User, get, http.HandlerFunc(a.handleRequestSearch))
	R("/api/v1/requests", authz.User, []string{"GET", "POST"}, http.HandlerFunc(a.handleRequests))
	R("/api/v1/notifications", authz.User, get, http.HandlerFunc(a.handleNotifications))
}

func (a *App) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handleCreateRequest(w, r)
		return
	}
	a.handleListRequests(w, r)
}
