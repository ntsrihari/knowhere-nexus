package catalog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/contract"
)

func logger(buf *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(buf, nil)) }

func TestLoadAtStartupValid(t *testing.T) {
	var logs bytes.Buffer
	h := NewHolder()
	h.LoadAtStartup("../../examples/catalog.yaml", contract.CatalogOptions{}, logger(&logs))
	if c := h.Get(); c.Revision != 1 || len(c.Apps) == 0 {
		t.Fatalf("catalog = %+v; logs %s", c, logs.String())
	}
}

func TestLoadAtStartupFailuresKeepEmpty(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(invalid, []byte("schema: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"missing": filepath.Join(dir, "none.yaml"), "invalid": invalid, "directory": dir} {
		var logs bytes.Buffer
		h := NewHolder()
		h.LoadAtStartup(p, contract.CatalogOptions{}, logger(&logs))
		if c := h.Get(); c.Revision != 0 || len(c.Apps) != 0 {
			t.Errorf("%s: catalog not empty: %+v", name, c)
		}
		if !strings.Contains(logs.String(), "empty catalog") {
			t.Errorf("%s: reason not logged: %s", name, logs.String())
		}
	}
}

func TestReplaceEnforcesRevision(t *testing.T) {
	h := NewHolder()
	c1 := &contract.Catalog{Schema: contract.CatalogSchema, SchemaVersion: "0.1.0", Revision: 2, Source: "file", UpdatedAt: "a"}
	if err := h.Replace(c1); err != nil {
		t.Fatal(err)
	}
	stale := *c1
	stale.Revision = 1
	if err := h.Replace(&stale); err == nil {
		t.Fatal("lower revision installed")
	}
	if h.Get() != c1 {
		t.Fatal("active catalog changed after rejected replace")
	}
}
