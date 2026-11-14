package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeRejectsBadConfigWithoutLeakingSecrets(t *testing.T) {
	dir := t.TempDir()
	marker := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte("sessionEncryptionKey: "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"bogus":          "listen: 127.0.0.1:18480\npublicOrigin: https://n.example\ndataDir: d\ncatalogPath: c.yaml\nsecretsFile: secrets.yaml\nbogus: 1\n",
		"trustedProxies": "listen: 127.0.0.1:18480\npublicOrigin: https://n.example\ndataDir: d\ncatalogPath: c.yaml\nsecretsFile: secrets.yaml\ntrustedProxies: [10.0.0.0/8]\n",
		"publicOrigin":   "listen: 127.0.0.1:18480\npublicOrigin: http://n.example\ndataDir: d\ncatalogPath: c.yaml\nsecretsFile: secrets.yaml\n",
	}
	for name, body := range cases {
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if code := run([]string{"serve", "-config", p}, &bytes.Buffer{}, &stderr); code == 0 {
			t.Errorf("%s: exit 0", name)
		}
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("%s: error does not name the field: %s", name, stderr.String())
		}
		if strings.Contains(stderr.String(), marker) {
			t.Errorf("%s: secret leaked: %s", name, stderr.String())
		}
	}
}

func TestServeRejectsLooseSecretsFile(t *testing.T) {
	dir := t.TempDir()
	marker := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))
	sec := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(sec, []byte("sessionEncryptionKey: "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sec, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:18480\npublicOrigin: https://n.example\ndataDir: d\ncatalogPath: c.yaml\nsecretsFile: secrets.yaml\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"serve", "-config", cfg}, &bytes.Buffer{}, &stderr); code == 0 {
		t.Fatal("exit 0 with world-readable secrets")
	}
	if strings.Contains(stderr.String(), marker) || !strings.Contains(stderr.String(), "secrets file") {
		t.Fatalf("stderr: %s", stderr.String())
	}
}
