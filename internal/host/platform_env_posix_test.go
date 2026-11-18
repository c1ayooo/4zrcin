//go:build !windows

package host

import (
	"testing"
)

// POSIX side of the PATH equivalence: the Windows Git-for-Windows fixup is not
// applied, so the child inherits the operator's PATH exactly. The Windows
// counterpart (platform_env_windows_test.go) asserts the fixup half; both tests
// carry the same semantic: "the host gets the toolchain the platform requires,
// and nothing else".
func TestChildEnvPathFixupOnPOSIX(t *testing.T) {
	want := "/usr/local/bin:/usr/bin:/bin"
	t.Setenv("PATH", want)
	h := &Host{cfg: Config{Logf: func(string, ...any) {}}}
	if got := envValue(h.childEnv(nil), "PATH"); got != want {
		t.Fatalf("PATH = %q, want it unchanged %q", got, want)
	}
}
