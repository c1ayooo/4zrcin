package api

import (
	"os"
	"testing"

	"github.com/kksty/tianwu/internal/testutil"
)

// TestMain installs the in-process host transport and the narrow dial-level
// retry, then reports how often a real dial had to be retried. See
// docs/01-architecture.md §9 for why the retry exists and its limits.
func TestMain(m *testing.M) {
	testutil.Install()
	code := m.Run()
	testutil.Summary()
	os.Exit(code)
}
