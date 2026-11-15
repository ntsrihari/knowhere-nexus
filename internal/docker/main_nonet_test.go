package docker

import (
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/testutil"
)

// ST-11: no outbound network during tests.
func TestMain(m *testing.M) { testutil.RunWithoutNetwork(m) }
