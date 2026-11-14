package contract

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testdata = "../../contracts/testdata"

func fixtures(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(testdata, dir))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(testdata, dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	if len(out) == 0 {
		t.Fatalf("no fixtures in %s", dir)
	}
	return out
}

// expectedCode reads the error code from an invalid fixture's name:
// "<code>--<description>.<ext>".
func expectedCode(t *testing.T, name string) string {
	t.Helper()
	code, _, ok := strings.Cut(name, "--")
	if !ok {
		t.Fatalf("fixture %s must be named <code>--<description>", name)
	}
	return code
}

func assertCode(t *testing.T, name string, err error, want string) {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Errorf("%s: want contract error %s, got %v", name, want, err)
		return
	}
	if ce.Code != want {
		t.Errorf("%s: code %s, want %s (%v)", name, ce.Code, want, ce)
	}
}

var catOpts = CatalogOptions{}
var themeOpts = ThemeOptions{Fonts: DefaultFonts}

func TestSchemasCompile(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogFixtures(t *testing.T) {
	for name, b := range fixtures(t, "catalog/valid") {
		c, err := ParseCatalog(b, catOpts)
		if err != nil {
			t.Errorf("valid %s rejected: %v", name, err)
			continue
		}
		if c.Schema != CatalogSchema || c.SchemaVersion == "" || c.Revision < 1 {
			t.Errorf("valid %s decoded badly: %+v", name, c)
		}
	}
	for name, b := range fixtures(t, "catalog/invalid") {
		_, err := ParseCatalog(b, catOpts)
		if err == nil {
			t.Errorf("invalid %s accepted", name)
			continue
		}
		assertCode(t, name, err, expectedCode(t, name))
	}
}

func TestThemeFixtures(t *testing.T) {
	for name, b := range fixtures(t, "theme/valid") {
		if _, err := ParseTheme(b, themeOpts); err != nil {
			t.Errorf("valid %s rejected: %v", name, err)
		}
	}
	for name, b := range fixtures(t, "theme/invalid") {
		_, err := ParseTheme(b, themeOpts)
		if err == nil {
			t.Errorf("invalid %s accepted", name)
			continue
		}
		assertCode(t, name, err, expectedCode(t, name))
	}
}

func TestNewerMinorDropsUnknownButKeepsKnown(t *testing.T) {
	b := fixtures(t, "catalog/valid")["newer-minor-unknown-fields.yaml"]
	c, err := ParseCatalog(b, catOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Apps) != 1 || c.Apps[0].ID != "jellyfin" || c.Apps[0].Extensions["collector"] == nil {
		t.Fatalf("known fields lost: %+v", c.Apps)
	}
	th, err := ParseTheme(fixtures(t, "theme/valid")["newer-minor.json"], themeOpts)
	if err != nil || th.SeedColor != "#6750A4" {
		t.Fatalf("theme newer minor: %+v %v", th, err)
	}
}

func TestSizeCaps(t *testing.T) {
	big := []byte("schema: knowhere.catalog\n#" + strings.Repeat("x", CatalogMaxSize))
	assertCode(t, "big catalog", errOnly(ParseCatalog(big, catOpts)), CodeTooLarge)
	bigTheme := []byte(`{"schema":"knowhere.theme","name":"` + strings.Repeat("x", ThemeMaxSize) + `"}`)
	assertCode(t, "big theme", errOnly(ParseTheme(bigTheme, themeOpts)), CodeTooLarge)

	var sb strings.Builder
	sb.WriteString("schema: knowhere.catalog\nschemaVersion: 0.1.0\nrevision: 1\nsource: file\nupdatedAt: 2026-11-14T00:00:00Z\napps:\n")
	for i := 0; i < 201; i++ {
		fmt.Fprintf(&sb, "  - {id: app%d, name: A, kind: server, icon: {builtin: x}, audience: all}\n", i)
	}
	assertCode(t, "201 apps", errOnly(ParseCatalog([]byte(sb.String()), catOpts)), CodeSchemaViolation)
}

func errOnly[T any](_ T, err error) error { return err }

func TestAuthorizedRouteAccepted(t *testing.T) {
	doc := "schema: knowhere.catalog\nschemaVersion: 0.1.0\nrevision: 1\nsource: file\nupdatedAt: 2026-11-14T00:00:00Z\napps:\n  - {id: jf, name: J, kind: server, icon: {builtin: j}, audience: all, launch: {route: jellyfin}}\n"
	if _, err := ParseCatalog([]byte(doc), CatalogOptions{KnownRoutes: map[string]bool{"jellyfin": true}}); err != nil {
		t.Fatal(err)
	}
}

func TestVersionParsing(t *testing.T) {
	for _, ok := range []string{"0.1.0", "0.0.0", "10.20.30"} {
		if _, err := ParseVersion(ok); err != nil {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "0.1", "v0.1.0", "01.1.0", "0.1.0-rc1", "0.1.0+b", " 0.1.0", "1234567.0.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	s := support{Major: 0, Minor: 1}
	for v, want := range map[string]mode{"0.0.5": strict, "0.1.0": strict, "0.1.9": strict, "0.2.0": lenient} {
		m, err := s.modeFor(mustVersion(t, v))
		if err != nil || m != want {
			t.Errorf("%s: mode %v err %v", v, m, err)
		}
	}
	if _, err := s.modeFor(mustVersion(t, "1.0.0")); err == nil {
		t.Error("major 1 accepted")
	}
}

func mustVersion(t *testing.T, s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckSuccessor(t *testing.T) {
	base := &Catalog{Schema: CatalogSchema, SchemaVersion: "0.1.0", Revision: 5, Source: "file", UpdatedAt: "x"}
	same := *base
	changed := *base
	changed.Source = "collector"
	higher := changed
	higher.Revision = 6
	lower := *base
	lower.Revision = 4

	if err := CheckSuccessor(nil, base); err != nil {
		t.Errorf("first load: %v", err)
	}
	if err := CheckSuccessor(base, &same); err != nil {
		t.Errorf("identical reload: %v", err)
	}
	if err := CheckSuccessor(base, &higher); err != nil {
		t.Errorf("higher revision: %v", err)
	}
	assertCode(t, "same revision, new content", CheckSuccessor(base, &changed), CodeStaleRevision)
	assertCode(t, "lower revision", CheckSuccessor(base, &lower), CodeStaleRevision)
}

func TestStrictJSON(t *testing.T) {
	deep := strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)
	for _, bad := range []string{`{"a":1,"a":2}`, `{"a":1} x`, deep, `{"a":}`, ``} {
		if _, err := decodeJSONValue([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	v, err := decodeJSONValue([]byte(`{"i":3,"f":1.5,"s":"x","b":true,"n":null,"l":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["i"] != int64(3) || m["f"] != 1.5 {
		t.Fatalf("numbers: %#v", m)
	}
}

func FuzzParseCatalog(f *testing.F) {
	for _, dir := range []string{"catalog/valid", "catalog/invalid"} {
		entries, _ := os.ReadDir(filepath.Join(testdata, dir))
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(testdata, dir, e.Name()))
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := ParseCatalog(data, catOpts)
		if err == nil && (c == nil || c.Schema != CatalogSchema || len(c.Apps) > 200) {
			t.Fatalf("accepted invalid catalog: %+v", c)
		}
	})
}

func FuzzParseTheme(f *testing.F) {
	for _, dir := range []string{"theme/valid", "theme/invalid"} {
		entries, _ := os.ReadDir(filepath.Join(testdata, dir))
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(testdata, dir, e.Name()))
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		th, err := ParseTheme(data, themeOpts)
		if err == nil && (th == nil || th.CornerRadius < 0 || th.CornerRadius > 32 || !themeOpts.Fonts[th.Font]) {
			t.Fatalf("accepted invalid theme: %+v", th)
		}
	})
}

func TestDefaultThemeAndExampleCatalogValidate(t *testing.T) {
	if _, err := DefaultTheme(); err != nil {
		t.Fatalf("default theme: %v", err)
	}
	b, err := os.ReadFile("../../examples/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(b, catOpts); err != nil {
		t.Fatalf("examples/catalog.yaml: %v", err)
	}
}
