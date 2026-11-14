package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ntsrihari/knowhere-nexus/contracts"
	"github.com/ntsrihari/knowhere-nexus/internal/safeyaml"
)

// Catalog contract constants (docs/CONTRACTS.md §1).
const (
	CatalogSchema  = "knowhere.catalog"
	CatalogMaxSize = 256 << 10
)

var catalogSupport = support{Major: 0, Minor: 1}

// catalogFields lists the fields of knowhere.catalog 0.1 (for pruning newer
// minors). extensions are kept opaque.
var catalogFields = func() *fieldSet {
	top := keep("schema", "schemaVersion", "revision", "source", "updatedAt")
	app := keep("id", "name", "kind", "description", "audience", "extensions")
	app["icon"] = &fieldSet{fields: keep("builtin")}
	app["launch"] = &fieldSet{fields: keep("route")}
	app["health"] = &fieldSet{fields: keep("containers")}
	top["apps"] = &fieldSet{items: &fieldSet{fields: app}}
	return &fieldSet{fields: top}
}()

// Catalog is a validated knowhere.catalog document.
type Catalog struct {
	Schema        string `json:"schema"`
	SchemaVersion string `json:"schemaVersion"`
	Revision      int64  `json:"revision"`
	Source        string `json:"source"`
	UpdatedAt     string `json:"updatedAt"`
	Apps          []App  `json:"apps"`
}

// App is one catalog entry.
type App struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Kind        string                    `json:"kind"`
	Description string                    `json:"description,omitempty"`
	Icon        Icon                      `json:"icon"`
	Audience    string                    `json:"audience"`
	Launch      *Launch                   `json:"launch,omitempty"`
	Health      *Health                   `json:"health,omitempty"`
	Extensions  map[string]map[string]any `json:"extensions,omitempty"`
}

// Icon references a built-in icon (Phase 1 allows nothing else).
type Icon struct {
	Builtin string `json:"builtin"`
}

// Launch names the authorized route the entry opens, if any.
type Launch struct {
	Route *string `json:"route"`
}

// Health lists the containers whose state makes up the up/down summary.
type Health struct {
	Containers []string `json:"containers"`
}

// CatalogOptions carries server-side context for semantic checks.
type CatalogOptions struct {
	// KnownRoutes is the authorized route registry (R-06). An entry whose
	// launch.route is not in it is rejected. Empty in M1.
	KnownRoutes map[string]bool
}

// ParseCatalog validates a catalog document (YAML or JSON) and returns it.
func ParseCatalog(data []byte, opt CatalogOptions) (*Catalog, error) {
	if len(data) > CatalogMaxSize {
		return nil, newErr(CodeTooLarge, "catalog exceeds %d bytes", CatalogMaxSize)
	}
	value, err := safeyaml.ToValue(data)
	if err != nil {
		return nil, newErr(CodeInvalidDocument, "%v", err)
	}
	var c Catalog
	if err := validateDocument(value, CatalogSchema, contracts.CatalogSchemaFile, catalogSupport, catalogFields, &c); err != nil {
		return nil, err
	}
	if err := c.semantic(opt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) semantic(opt CatalogOptions) error {
	e := &Error{Code: CodeInvalidValue}
	if _, err := time.Parse(time.RFC3339, c.UpdatedAt); err != nil {
		e.Problems = append(e.Problems, "/updatedAt: must be an RFC 3339 timestamp")
	}
	seen := map[string]bool{}
	for i, a := range c.Apps {
		if seen[a.ID] {
			e.Problems = append(e.Problems, fmt.Sprintf("/apps/%d/id: duplicate id %q", i, a.ID))
		}
		seen[a.ID] = true
		if a.Launch != nil && a.Launch.Route != nil && !opt.KnownRoutes[*a.Launch.Route] {
			e.Problems = append(e.Problems, fmt.Sprintf("/apps/%d/launch/route: %q is not an authorized route", i, *a.Launch.Route))
		}
		if len(e.Problems) >= maxProblems {
			break
		}
	}
	if len(e.Problems) > 0 {
		return e
	}
	return nil
}

// Digest is a stable hash of the catalog's validated content.
func (c *Catalog) Digest() string {
	b, _ := json.Marshal(c) // field order is fixed by the struct
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// CheckSuccessor applies the revision rule: a replacement must carry a higher
// revision; the same revision is accepted only if the content is identical
// (a no-op reload). prev may be nil for the first load.
func CheckSuccessor(prev, next *Catalog) error {
	if prev == nil || prev.Revision == 0 {
		return nil
	}
	switch {
	case next.Revision > prev.Revision:
		return nil
	case next.Revision == prev.Revision && next.Digest() == prev.Digest():
		return nil
	case next.Revision == prev.Revision:
		return newErr(CodeStaleRevision, "revision %d is already loaded with different content; increase revision", next.Revision)
	default:
		return newErr(CodeStaleRevision, "revision %d is lower than the loaded revision %d", next.Revision, prev.Revision)
	}
}
