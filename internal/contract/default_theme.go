package contract

import _ "embed"

//go:embed default_theme.json
var defaultThemeJSON []byte

// DefaultTheme returns the built-in household default theme (S-15: the last
// step of theme precedence). It is validated on every call.
func DefaultTheme() (*Theme, error) {
	return ParseTheme(defaultThemeJSON, ThemeOptions{Fonts: DefaultFonts})
}
