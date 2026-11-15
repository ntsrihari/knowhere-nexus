// Package config loads and validates the non-secret Nexus configuration.
//
// Decoding is strict: unknown keys, type mismatches, duplicate keys, anchors
// and aliases are errors. Error messages name fields but never echo values
// from the secrets file (secrets live in a separate file, see package secrets).
package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/internal/safeyaml"
)

// MaxFileSize bounds the configuration file.
const MaxFileSize = 64 << 10

// File is the on-disk shape of config.yaml.
type File struct {
	Listen         string   `yaml:"listen"`
	PublicOrigin   string   `yaml:"publicOrigin"`
	TrustedProxies []string `yaml:"trustedProxies"`
	DataDir        string   `yaml:"dataDir"`
	CatalogPath    string   `yaml:"catalogPath"`
	SecretsFile    string   `yaml:"secretsFile"`
	LogLevel       string   `yaml:"logLevel"`
	// RequireTailnet rejects clients outside Tailscale address ranges (R-15).
	// Defaults to true; set false only for local development.
	RequireTailnet *bool            `yaml:"requireTailnet"`
	Jellyfin       EndpointFile     `yaml:"jellyfin"`
	Docker         EndpointFile     `yaml:"docker"`
	Storage        StorageFile      `yaml:"storage"`
	Integrations   IntegrationsFile `yaml:"integrations"`
	Routes         []RouteFile      `yaml:"routes"`
	Services       []ServiceFile    `yaml:"services"`
}

// EndpointFile is a downstream base URL.
type EndpointFile struct {
	URL string `yaml:"url"`
}

// StorageFile lists mounts reported by the storage widget (R-13).
type StorageFile struct {
	Mounts   []MountFile `yaml:"mounts"`
	Excluded []string    `yaml:"excluded"`
}

// MountFile is one monitored mount: Path is where Nexus can see it.
type MountFile struct {
	Label string `yaml:"label"`
	Path  string `yaml:"path"`
	// HostPath is the path on the host, matched in mountinfo (defaults to Path).
	HostPath string `yaml:"hostPath"`
}

// IntegrationsFile holds optional downstream service endpoints.
type IntegrationsFile struct {
	Immich      EndpointFile `yaml:"immich"`
	Pihole      EndpointFile `yaml:"pihole"`
	QBittorrent EndpointFile `yaml:"qbittorrent"`
	Radarr      EndpointFile `yaml:"radarr"`
	Sonarr      EndpointFile `yaml:"sonarr"`
	Prowlarr    EndpointFile `yaml:"prowlarr"`
	Jellyseerr  EndpointFile `yaml:"jellyseerr"`
}

// RouteFile is an authorized reverse-proxy route (R-06, A-02).
type RouteFile struct {
	ID         string `yaml:"id"`
	URL        string `yaml:"url"`
	AuthMode   string `yaml:"authMode"`
	Audience   string `yaml:"audience"`
	ApprovedBy string `yaml:"approvedBy"`
	ApprovedOn string `yaml:"approvedOn"`
}

// ServiceFile registers a service identity for service tokens (R-03).
type ServiceFile struct {
	ID        string   `yaml:"id"`
	PublicKey string   `yaml:"publicKey"` // base64 Ed25519 public key
	Scopes    []string `yaml:"scopes"`
	Disabled  bool     `yaml:"disabled"`
}

// Config is the validated configuration.
type Config struct {
	Listen         string
	PublicOrigin   string // normalised scheme://host[:port]
	TrustedProxies []netip.Addr
	DataDir        string // absolute
	CatalogPath    string // absolute
	SecretsFile    string // absolute
	LogLevel       slog.Level
	RequireTailnet bool
	JellyfinURL    string // empty: not configured
	DockerURL      string // empty: not configured
	Mounts         []Mount
	Excluded       []string
	Integrations   map[string]string // name -> base URL (configured only)
	Routes         []Route
	Services       []Service
}

// Mount is a validated monitored mount.
type Mount struct{ Label, Path, HostPath string }

// Route is a validated authorized route.
type Route struct{ ID, URL, AuthMode, Audience, ApprovedBy, ApprovedOn string }

// Service is a validated service identity.
type Service struct {
	ID        string
	PublicKey []byte
	Scopes    []string
	Disabled  bool
}

// ValidationError lists every problem found, one per field.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Load reads, decodes and validates the file at path. Relative paths inside
// the file are resolved against the directory containing it.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config path: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("config file %s is not a regular file", abs)
	}
	if st.Size() > MaxFileSize {
		return nil, fmt.Errorf("config file %s exceeds %d bytes", abs, MaxFileSize)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}
	return Parse(data, filepath.Dir(abs))
}

// Parse decodes and validates data; baseDir resolves relative paths.
func Parse(data []byte, baseDir string) (*Config, error) {
	var f File
	if err := safeyaml.DecodeStrict(data, &f); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return f.validate(baseDir)
}

func (f *File) validate(baseDir string) (*Config, error) {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	c := &Config{}

	// listen
	if f.Listen == "" {
		add("listen: required (host:port)")
	} else if host, port, err := net.SplitHostPort(f.Listen); err != nil {
		add("listen: must be host:port")
	} else {
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			add("listen: port must be 1-65535")
		}
		if host != "" {
			if _, err := netip.ParseAddr(host); err != nil {
				add("listen: host must be an IP address (or empty for all interfaces)")
			}
		}
		c.Listen = f.Listen
	}

	// publicOrigin
	if origin, err := normaliseOrigin(f.PublicOrigin); err != nil {
		add("publicOrigin: %v", err)
	} else {
		c.PublicOrigin = origin
	}

	// trustedProxies: exact single hosts only (R-15).
	seen := map[netip.Addr]bool{}
	for i, raw := range f.TrustedProxies {
		addr, err := parseSingleHost(raw)
		if err != nil {
			add("trustedProxies[%d]: %v", i, err)
			continue
		}
		if !seen[addr] {
			seen[addr] = true
			c.TrustedProxies = append(c.TrustedProxies, addr)
		}
	}

	// paths
	resolve := func(field, v string) string {
		if v == "" {
			add("%s: required", field)
			return ""
		}
		if strings.ContainsAny(v, "\x00?#%") {
			add("%s: must not contain NUL, '?', '#' or '%%'", field)
			return ""
		}
		if !filepath.IsAbs(v) {
			v = filepath.Join(baseDir, v)
		}
		return filepath.Clean(v)
	}
	c.DataDir = resolve("dataDir", f.DataDir)
	c.CatalogPath = resolve("catalogPath", f.CatalogPath)
	c.SecretsFile = resolve("secretsFile", f.SecretsFile)

	// logLevel
	switch strings.ToLower(f.LogLevel) {
	case "", "info":
		c.LogLevel = slog.LevelInfo
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		add("logLevel: must be one of debug, info, warn, error")
	}

	c.RequireTailnet = f.RequireTailnet == nil || *f.RequireTailnet
	c.JellyfinURL = checkURL("jellyfin.url", f.Jellyfin.URL, add)
	c.DockerURL = checkURL("docker.url", f.Docker.URL, add)
	c.Integrations = map[string]string{}
	for name, ep := range map[string]EndpointFile{
		"immich": f.Integrations.Immich, "pihole": f.Integrations.Pihole,
		"qbittorrent": f.Integrations.QBittorrent, "radarr": f.Integrations.Radarr,
		"sonarr": f.Integrations.Sonarr, "prowlarr": f.Integrations.Prowlarr,
		"jellyseerr": f.Integrations.Jellyseerr,
	} {
		if u := checkURL("integrations."+name+".url", ep.URL, add); u != "" {
			c.Integrations[name] = u
		}
	}
	for i, m := range f.Storage.Mounts {
		if m.Label == "" || len(m.Label) > 40 {
			add("storage.mounts[%d].label: required, at most 40 characters", i)
		}
		if !filepath.IsAbs(m.Path) || strings.ContainsAny(m.Path, "\x00") {
			add("storage.mounts[%d].path: must be an absolute path", i)
		}
		hp := m.HostPath
		if hp == "" {
			hp = m.Path
		}
		c.Mounts = append(c.Mounts, Mount{Label: m.Label, Path: filepath.Clean(m.Path), HostPath: filepath.Clean(hp)})
	}
	c.Excluded = []string{"/mnt/internal-hdd"}
	for i, e := range f.Storage.Excluded {
		if !filepath.IsAbs(e) {
			add("storage.excluded[%d]: must be an absolute path", i)
			continue
		}
		c.Excluded = append(c.Excluded, filepath.Clean(e))
	}
	idRe := regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	seenRoute := map[string]bool{}
	for i, r := range f.Routes {
		if !idRe.MatchString(r.ID) || seenRoute[r.ID] {
			add("routes[%d].id: must be a unique lowercase id", i)
		}
		seenRoute[r.ID] = true
		if r.AuthMode != "nexus" && r.AuthMode != "upstream" {
			add("routes[%d].authMode: must be nexus or upstream", i)
		}
		if r.ApprovedBy == "" || r.ApprovedOn == "" {
			add("routes[%d]: approvedBy and approvedOn are required (A-02)", i)
		}
		u := r.URL
		if !strings.HasPrefix(u, "/") {
			u = checkURL(fmt.Sprintf("routes[%d].url", i), r.URL, add)
		}
		c.Routes = append(c.Routes, Route{ID: r.ID, URL: u, AuthMode: r.AuthMode, Audience: r.Audience, ApprovedBy: r.ApprovedBy, ApprovedOn: r.ApprovedOn})
	}
	seenSvc := map[string]bool{}
	for i, sv := range f.Services {
		if !idRe.MatchString(sv.ID) || seenSvc[sv.ID] {
			add("services[%d].id: must be a unique lowercase id", i)
		}
		seenSvc[sv.ID] = true
		key, err := base64.StdEncoding.DecodeString(sv.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			add("services[%d].publicKey: must be a base64 Ed25519 public key", i)
		}
		c.Services = append(c.Services, Service{ID: sv.ID, PublicKey: key, Scopes: sv.Scopes, Disabled: sv.Disabled})
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return c, nil
}

// checkURL validates an optional http(s) base URL without credentials,
// query or fragment and returns it without a trailing slash.
func checkURL(field, raw string, add func(string, ...any)) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		add("%s: must be an http(s) URL without credentials, query or fragment", field)
		return ""
	}
	return strings.TrimRight(u.String(), "/")
}

func normaliseOrigin(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("required (for example https://knowhere.example.ts.net)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("not a valid URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("must not contain credentials, query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("must not contain a path")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("must include a host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain HTTP only for local development.
		if !isLocalhost(host) {
			return "", errors.New("must use https (http is allowed only for localhost)")
		}
	default:
		return "", errors.New("scheme must be https")
	}
	origin := u.Scheme + "://" + host
	if strings.Contains(host, ":") {
		origin = u.Scheme + "://[" + host + "]"
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid port")
		}
		origin += ":" + p
	}
	return origin, nil
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func parseSingleHost(raw string) (netip.Addr, error) {
	if strings.Contains(raw, "/") {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return netip.Addr{}, errors.New("not a valid IP or CIDR")
		}
		if p.Bits() != p.Addr().BitLen() {
			return netip.Addr{}, errors.New("must be a single host (/32 or /128)")
		}
		return p.Addr().Unmap(), nil
	}
	a, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, errors.New("not a valid IP address")
	}
	if a.Zone() != "" {
		return netip.Addr{}, errors.New("zoned addresses are not allowed")
	}
	return a.Unmap(), nil
}
