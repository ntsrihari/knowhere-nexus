// Package buildinfo carries version information injected at build time with
// -ldflags "-X github.com/ntsrihari/knowhere-nexus/internal/buildinfo.Version=…".
package buildinfo

import "fmt"

var (
	// Version is the release version, or "dev" for local builds.
	Version = "dev"
	// Commit is the source commit the binary was built from.
	Commit = "unknown"
)

// String returns a one-line description of the build.
func String() string {
	return fmt.Sprintf("knowhere-nexus %s (commit %s)", Version, Commit)
}
