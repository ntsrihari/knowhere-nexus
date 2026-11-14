package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// outboundAllowed lists the non-test files allowed to create outbound network
// connections. Adding a file here is a reviewed decision (P-12, R-09).
var outboundAllowed = map[string]bool{
	"cmd/nexus/healthcheck.go":   true, // loopback-only container healthcheck
	"internal/testutil/nonet.go": true,
}

var outboundRe = regexp.MustCompile(`\bnet\.Dial|\bnet\.DialTCP|\bnet\.DialUDP|\btls\.Dial|` +
	`\bhttp\.Transport\{|\bhttp\.Client\{|\bhttp\.Get\(|\bhttp\.Post\(|\bhttp\.Head\(|` +
	`\bhttp\.PostForm\(|\bhttp\.DefaultClient\b|\bhttp\.DefaultTransport\b|\bnet\.Dialer\{`)

// ST-11 (static half): the runtime guard only covers the default transport
// and resolver, so production code may dial out only from allowlisted files.
func TestNoUnreviewedOutboundNetworkCode(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal", "contracts"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if loc := outboundRe.Find(b); loc != nil && !outboundAllowed[rel] {
				t.Errorf("%s uses %q: outbound network code must be allowlisted in %s", rel, loc, "internal/testutil/outbound_gate_test.go")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
