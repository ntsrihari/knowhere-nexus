package config

import (
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `
listen: 127.0.0.1:8480
publicOrigin: https://Knowhere.Example.ts.net/
trustedProxies: [172.20.0.2, "172.20.0.3/32", "::ffff:172.20.0.2"]
dataDir: data
catalogPath: /etc/nexus/catalog.yaml
secretsFile: ./secrets.yaml
logLevel: debug
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(valid), "/etc/nexus")
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicOrigin != "https://knowhere.example.ts.net" {
		t.Errorf("origin = %q", c.PublicOrigin)
	}
	want := []netip.Addr{netip.MustParseAddr("172.20.0.2"), netip.MustParseAddr("172.20.0.3")}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[0] != want[0] || c.TrustedProxies[1] != want[1] {
		t.Errorf("trustedProxies = %v (mapped duplicate must be removed)", c.TrustedProxies)
	}
	if c.DataDir != "/etc/nexus/data" || c.SecretsFile != "/etc/nexus/secrets.yaml" || c.CatalogPath != "/etc/nexus/catalog.yaml" {
		t.Errorf("paths = %q %q %q", c.DataDir, c.SecretsFile, c.CatalogPath)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Errorf("level = %v", c.LogLevel)
	}
}

func TestParseRejects(t *testing.T) {
	base := "listen: 127.0.0.1:8480\npublicOrigin: https://n.example\ndataDir: /d\ncatalogPath: /c\nsecretsFile: /s\n"
	cases := map[string]string{
		"unknown key":          base + "extra: 1\n",
		"misspelt key":         strings.Replace(base, "dataDir", "datadir", 1),
		"type mismatch":        base + "trustedProxies: 10.0.0.1\n",
		"duplicate key":        base + "listen: 127.0.0.1:1\n",
		"anchor":               base + "logLevel: &l info\n",
		"missing listen":       strings.Replace(base, "listen: 127.0.0.1:8480\n", "", 1),
		"bad port":             strings.Replace(base, ":8480", ":70000", 1),
		"hostname listen":      strings.Replace(base, "127.0.0.1:8480", "localhost:8480", 1),
		"http origin":          strings.Replace(base, "https://n.example", "http://n.example", 1),
		"origin with path":     strings.Replace(base, "https://n.example", "https://n.example/app", 1),
		"origin with userinfo": strings.Replace(base, "https://n.example", "https://u:p@n.example", 1),
		"origin with query":    strings.Replace(base, "https://n.example", "https://n.example/?a=1", 1),
		"ftp origin":           strings.Replace(base, "https://n.example", "ftp://n.example", 1),
		"proxy subnet":         base + "trustedProxies: [10.0.0.0/24]\n",
		"proxy hostname":       base + "trustedProxies: [caddy]\n",
		"proxy zone":           base + "trustedProxies: [\"fe80::1%eth0\"]\n",
		"bad log level":        base + "logLevel: verbose\n",
		"missing secretsFile":  strings.Replace(base, "secretsFile: /s\n", "", 1),
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc), "/"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHTTPLocalhostOriginAllowed(t *testing.T) {
	doc := "listen: 127.0.0.1:8480\npublicOrigin: http://localhost:8480\ndataDir: /d\ncatalogPath: /c\nsecretsFile: /s\n"
	c, err := Parse([]byte(doc), "/")
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicOrigin != "http://localhost:8480" {
		t.Fatalf("origin = %q", c.PublicOrigin)
	}
}

func TestValidationErrorListsEveryProblem(t *testing.T) {
	_, err := Parse([]byte("listen: nope\npublicOrigin: http://x\n"), "/")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if len(ve.Problems) < 5 {
		t.Fatalf("want all problems reported, got %v", ve.Problems)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(dir, "data") {
		t.Errorf("relative dataDir resolved to %q", c.DataDir)
	}
	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, make([]byte, MaxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err == nil {
		t.Error("oversized file accepted")
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := Load(dir); err == nil {
		t.Error("directory accepted")
	}
}
