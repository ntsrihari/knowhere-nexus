// Package webui serves the embedded SvelteKit build (P-03).
//
// The build is copied into dist/ by `make frontend` before `go build`. Files
// are served from the embedded filesystem only; directories are never listed;
// unknown paths without a file extension fall back to index.html (SPA
// routing); unknown asset paths are 404.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/httpx"
)

//go:embed all:dist
var embedded embed.FS

// Embedded returns the embedded build output.
func Embedded() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Site serves a static build.
type Site struct {
	fsys   fs.FS
	index  []byte
	hashes []string
}

// New prepares a Site. A build without index.html is allowed (the frontend
// was not built); the site then answers 503 for pages.
func New(fsys fs.FS) (*Site, error) {
	s := &Site{fsys: fsys}
	b, err := fs.ReadFile(fsys, "index.html")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	s.index = b
	s.hashes = InlineScriptHashes(b)
	return s, nil
}

// Built reports whether a frontend build is present.
func (s *Site) Built() bool { return s.index != nil }

// ScriptHashes returns CSP source expressions ('sha256-…') for every inline
// script in index.html.
func (s *Site) ScriptHashes() []string { return append([]string(nil), s.hashes...) }

var (
	scriptRe = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script\s*>`)
	srcRe    = regexp.MustCompile(`(?i)\bsrc\s*=`)
)

// InlineScriptHashes computes the CSP hash of each inline <script> element's
// exact text. External scripts (with src) are covered by 'self'.
func InlineScriptHashes(html []byte) []string {
	var out []string
	for _, m := range scriptRe.FindAllSubmatch(html, -1) {
		if srcRe.Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
	".map":   "application/json",
}

// ServeHTTP implements http.Handler. Only GET and HEAD reach it (the route
// table enforces methods).
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Path
	if strings.ContainsAny(raw, "\\\x00") {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "not found")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if name == "" || name == "index.html" {
		s.serveIndex(w, r)
		return
	}
	if !fs.ValidPath(name) || strings.HasPrefix(path.Base(name), ".") {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "not found")
		return
	}
	f, err := s.fsys.Open(name)
	if err != nil {
		s.fallback(w, r, name)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		// Never list directories.
		s.fallback(w, r, name)
		return
	}
	ext := path.Ext(name)
	ct, ok := contentTypes[ext]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(name, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", time.Time{}, rs)
		return
	}
	b, err := io.ReadAll(f)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}

// fallback serves index.html for client-side routes and 404 for missing
// assets, so a missing script never comes back as HTML.
func (s *Site) fallback(w http.ResponseWriter, r *http.Request, name string) {
	if name == "_app" || strings.HasPrefix(name, "_app/") || path.Ext(name) != "" {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "not found")
		return
	}
	s.serveIndex(w, r)
}

func (s *Site) serveIndex(w http.ResponseWriter, r *http.Request) {
	if s.index == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "frontend_not_built", "the web interface is not included in this build")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(s.index))
}
