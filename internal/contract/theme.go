package contract

import (
	"fmt"
	"strings"

	"github.com/ntsrihari/knowhere-nexus/contracts"
)

// Theme contract constants (docs/CONTRACTS.md §2).
const (
	ThemeSchema  = "knowhere.theme"
	ThemeMaxSize = 4 << 10
)

var themeSupport = support{Major: 0, Minor: 1}

// themeFields lists the fields of knowhere.theme 0.1 (for pruning newer minors).
var themeFields = func() *fieldSet {
	top := keep("schema", "schemaVersion", "name", "seedColor", "colorScheme", "cornerRadius", "font", "density", "extensions")
	top["wallpaper"] = &fieldSet{fields: keep("ref")}
	return &fieldSet{fields: top}
}()

// DefaultFonts is the proposed bundled font allowlist (open question Q-06).
var DefaultFonts = map[string]bool{"inter": true, "roboto": true, "roboto-flex": true, "jetbrains-mono": true}

// Theme is a validated knowhere.theme document.
type Theme struct {
	Schema        string                    `json:"schema"`
	SchemaVersion string                    `json:"schemaVersion"`
	Name          string                    `json:"name,omitempty"`
	SeedColor     string                    `json:"seedColor"`
	ColorScheme   string                    `json:"colorScheme"`
	CornerRadius  int                       `json:"cornerRadius"`
	Font          string                    `json:"font"`
	Density       string                    `json:"density"`
	Wallpaper     *Wallpaper                `json:"wallpaper,omitempty"`
	Extensions    map[string]map[string]any `json:"extensions,omitempty"`
}

// Wallpaper references a built-in wallpaper (uploads wait for D-06).
type Wallpaper struct {
	Ref string `json:"ref"`
}

// ThemeOptions carries server-side context for semantic checks.
type ThemeOptions struct {
	Fonts      map[string]bool // bundled font keys
	Wallpapers map[string]bool // built-in wallpaper keys (empty in M1)
}

// ParseTheme validates a theme document (JSON) and returns it.
func ParseTheme(data []byte, opt ThemeOptions) (*Theme, error) {
	if len(data) > ThemeMaxSize {
		return nil, newErr(CodeTooLarge, "theme exceeds %d bytes", ThemeMaxSize)
	}
	value, err := decodeJSONValue(data)
	if err != nil {
		return nil, newErr(CodeInvalidDocument, "%v", err)
	}
	var t Theme
	if err := validateDocument(value, ThemeSchema, contracts.ThemeSchemaFile, themeSupport, themeFields, &t); err != nil {
		return nil, err
	}
	e := &Error{Code: CodeInvalidValue}
	if !opt.Fonts[t.Font] {
		e.Problems = append(e.Problems, fmt.Sprintf("/font: %q is not a bundled font", t.Font))
	}
	if t.Wallpaper != nil && !opt.Wallpapers[strings.TrimPrefix(t.Wallpaper.Ref, "builtin:")] {
		e.Problems = append(e.Problems, fmt.Sprintf("/wallpaper/ref: %q is not a built-in wallpaper", t.Wallpaper.Ref))
	}
	if len(e.Problems) > 0 {
		return nil, e
	}
	return &t, nil
}
