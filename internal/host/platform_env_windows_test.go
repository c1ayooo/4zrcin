//go:build windows

package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows side of the PATH equivalence: when the operator's PATH has no
// coreutils, the host prepends Git's usr/bin; when it does, the PATH is left
// byte-identical. The POSIX counterpart (platform_env_posix_test.go) asserts the
// other half of the same semantic: no rewrite at all.
func TestChildEnvPathFixupOnWindows(t *testing.T) {
	gitDir := filepath.Join(t.TempDir(), "Git", "usr", "bin")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "ls.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramFiles", filepath.Dir(filepath.Dir(filepath.Dir(gitDir))))
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	original := `C:\Windows\System32`
	t.Setenv("PATH", original)

	h := &Host{cfg: Config{Logf: func(string, ...any) {}}}
	env := h.childEnv(nil)
	path := envValue(env, "PATH")
	if path != gitDir+`;`+original {
		t.Fatalf("PATH = %q, want %q", path, gitDir+`;`+original)
	}
	// Exactly one PATH definition, or a child could read either value.
	n := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("PATH defined %d times", n)
	}
}
