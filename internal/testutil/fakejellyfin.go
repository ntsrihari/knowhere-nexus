package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
)

// FakeUser is a Jellyfin account in FakeJellyfin.
type FakeUser struct {
	ID, Name, Password string
	Admin, Disabled    bool
}

// FakeJellyfin is an in-memory Jellyfin authentication server for tests.
// It records every token it issues and every token logged out, so tests can
// prove Nexus discards Jellyfin sessions (R-10).
type FakeJellyfin struct {
	*httptest.Server
	mu        sync.Mutex
	users     map[string]*FakeUser // by name
	tokens    map[string]*FakeUser // active tokens
	LoggedOut []string
	Issued    []string
	qc        map[string]*fakeQC // by secret
	// AdminKey is accepted as an API key with admin rights (admin broker).
	AdminKey string
	// Sessions and Tasks are returned by admin endpoints.
	Sessions []map[string]any
	Tasks    []map[string]any
	TaskRuns []string
}

type fakeQC struct {
	code string
	user *FakeUser
}

var tokenRe = regexp.MustCompile(`Token="([^"]*)"`)

// NewFakeJellyfin starts the server.
func NewFakeJellyfin(users ...FakeUser) *FakeJellyfin {
	f := &FakeJellyfin{users: map[string]*FakeUser{}, tokens: map[string]*FakeUser{}, qc: map[string]*fakeQC{}}
	for i := range users {
		u := users[i]
		f.users[u.Name] = &u
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /Users/AuthenticateByName", f.authByName)
	mux.HandleFunc("POST /QuickConnect/Initiate", f.qcInitiate)
	mux.HandleFunc("GET /QuickConnect/Connect", f.qcConnect)
	mux.HandleFunc("POST /Users/AuthenticateWithQuickConnect", f.qcAuth)
	mux.HandleFunc("POST /Sessions/Logout", f.logout)
	mux.HandleFunc("GET /Users/Me", f.me)
	mux.HandleFunc("GET /Sessions", f.sessions)
	mux.HandleFunc("GET /ScheduledTasks", f.tasks)
	mux.HandleFunc("POST /ScheduledTasks/Running/{id}", f.runTask)
	f.Server = httptest.NewServer(mux)
	return f
}

func randHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (f *FakeJellyfin) issue(w http.ResponseWriter, u *FakeUser) {
	tok := randHex()
	f.tokens[tok] = u
	f.Issued = append(f.Issued, tok)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"AccessToken": tok,
		"User": map[string]any{"Id": u.ID, "Name": u.Name,
			"Policy": map[string]any{"IsAdministrator": u.Admin, "IsDisabled": u.Disabled}},
	})
}

func (f *FakeJellyfin) authByName(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Pw string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[body.Username]
	if u == nil || u.Password != body.Pw {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.issue(w, u)
}

func (f *FakeJellyfin) qcInitiate(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	secret := randHex()
	code := secret[:6]
	f.qc[secret] = &fakeQC{code: code}
	_ = json.NewEncoder(w).Encode(map[string]any{"Secret": secret, "Code": code, "Authenticated": false})
}

// ApproveQuickConnect approves the pending request with the given code.
func (f *FakeJellyfin) ApproveQuickConnect(code, userName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.qc {
		if q.code == code {
			q.user = f.users[userName]
			return q.user != nil
		}
	}
	return false
}

func (f *FakeJellyfin) qcConnect(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.qc[r.URL.Query().Get("secret")]
	if q == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"Authenticated": q.user != nil})
}

func (f *FakeJellyfin) qcAuth(w http.ResponseWriter, r *http.Request) {
	var body struct{ Secret string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.qc[body.Secret]
	if q == nil || q.user == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	delete(f.qc, body.Secret)
	f.issue(w, q.user)
}

func (f *FakeJellyfin) caller(r *http.Request) (*FakeUser, string) {
	m := tokenRe.FindStringSubmatch(r.Header.Get("Authorization"))
	tok := ""
	if m != nil {
		tok = m[1]
	}
	if tok == "" {
		tok = r.Header.Get("X-Emby-Token")
	}
	if f.AdminKey != "" && tok == f.AdminKey {
		return &FakeUser{ID: "apikey", Name: "api", Admin: true}, tok
	}
	return f.tokens[tok], tok
}

func (f *FakeJellyfin) logout(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, tok := f.caller(r)
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	delete(f.tokens, tok)
	f.LoggedOut = append(f.LoggedOut, tok)
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeJellyfin) me(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, _ := f.caller(r)
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"Id": u.ID, "Name": u.Name,
		"Policy": map[string]any{"IsAdministrator": u.Admin, "IsDisabled": u.Disabled}})
}

func (f *FakeJellyfin) admin(w http.ResponseWriter, r *http.Request) bool {
	u, _ := f.caller(r)
	if u == nil || !u.Admin {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

func (f *FakeJellyfin) sessions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.admin(w, r) {
		return
	}
	_ = json.NewEncoder(w).Encode(f.Sessions)
}

func (f *FakeJellyfin) tasks(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.admin(w, r) {
		return
	}
	_ = json.NewEncoder(w).Encode(f.Tasks)
}

func (f *FakeJellyfin) runTask(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.admin(w, r) {
		return
	}
	f.TaskRuns = append(f.TaskRuns, r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// ActiveTokens returns the number of tokens not yet logged out.
func (f *FakeJellyfin) ActiveTokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tokens)
}

// IssueToken creates a token directly (simulates a Milano login).
func (f *FakeJellyfin) IssueToken(userName string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := randHex()
	f.tokens[tok] = f.users[userName]
	return tok
}
