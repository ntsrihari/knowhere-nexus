// Package catalog holds the launcher catalog currently in force (A-09, A-20).
// In Phase 1 the source is the server-side catalog file; from Phase 2
// Collector publishes documents in the same contract.
package catalog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/ntsrihari/knowhere-nexus/internal/contract"
)

// Holder stores the active catalog. Reads are lock-free.
type Holder struct {
	mu  sync.Mutex
	cur atomic.Pointer[contract.Catalog]
}

// Empty is the catalog used when no valid file is available: revision 0, no apps.
func Empty() *contract.Catalog {
	return &contract.Catalog{Schema: contract.CatalogSchema, SchemaVersion: "0.1.0", Source: "file", Apps: []contract.App{}}
}

// NewHolder returns a holder containing the empty catalog.
func NewHolder() *Holder {
	h := &Holder{}
	h.cur.Store(Empty())
	return h
}

// Get returns the active catalog. Callers must not modify it.
func (h *Holder) Get() *contract.Catalog { return h.cur.Load() }

// Replace installs c if it passes the revision rule.
func (h *Holder) Replace(c *contract.Catalog) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := contract.CheckSuccessor(h.cur.Load(), c); err != nil {
		return err
	}
	h.cur.Store(c)
	return nil
}

// ReadFile reads and validates a catalog file.
func ReadFile(path string, opt contract.CatalogOptions) (*contract.Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, contract.CatalogMaxSize+1))
	if err != nil {
		return nil, err
	}
	return contract.ParseCatalog(data, opt)
}

// LoadAtStartup loads the catalog file into h. A missing or invalid file is
// not fatal (A-17): Nexus starts with the empty catalog and logs why.
func (h *Holder) LoadAtStartup(path string, opt contract.CatalogOptions, log *slog.Logger) {
	c, err := ReadFile(path, opt)
	if err != nil {
		log.Warn("catalog not loaded; starting with an empty catalog", "path", path, "reason", err.Error())
		return
	}
	if err := h.Replace(c); err != nil {
		log.Warn("catalog rejected; starting with an empty catalog", "path", path, "reason", err.Error())
		return
	}
	log.Info("catalog loaded", "path", path, "revision", c.Revision, "schemaVersion", c.SchemaVersion, "apps", len(c.Apps))
}
