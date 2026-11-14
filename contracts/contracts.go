// Package contracts embeds the JSON Schema files that are the source of truth
// for the Nexus data contracts (docs/CONTRACTS.md).
package contracts

import "embed"

// Schemas holds every *.schema.json file in this directory.
//
//go:embed *.schema.json
var Schemas embed.FS

// Schema file names.
const (
	CatalogSchemaFile = "knowhere.catalog-0.1.schema.json"
	ThemeSchemaFile   = "knowhere.theme-0.1.schema.json"
)
