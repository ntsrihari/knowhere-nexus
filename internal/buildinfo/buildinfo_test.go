package buildinfo

import (
	"strings"
	"testing"
)

func TestStringIncludesVersionAndCommit(t *testing.T) {
	oldV, oldC := Version, Commit
	t.Cleanup(func() { Version, Commit = oldV, oldC })
	Version, Commit = "1.2.3", "abc123"
	got := String()
	if !strings.Contains(got, "1.2.3") || !strings.Contains(got, "abc123") {
		t.Fatalf("String() = %q, want version and commit", got)
	}
}
