// Package config loads and validates the non-secret Nexus configuration.
//
// Decoding is strict: unknown keys, type mismatches, duplicate keys, anchors
// and aliases are errors. Error messages name fields but never echo values
// from the secrets file (secrets live in a separate file, see package secrets).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
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

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return c, nil
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
