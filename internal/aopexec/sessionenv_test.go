package aopexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/aop"
)

func TestSessionToolEnvMatchesCaseState(t *testing.T) {
	dir := t.TempDir()
	zrcin := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zrcin, "identity.json"), []byte(
		`{"api_base":"http://127.0.0.1:8080/","project_id":"p-1","intent_id":"in-9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zrcin, "auth"),
		[]byte("Authorization: Bearer tok-xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := sessionToolEnv(dir, "", 0)
	want := map[string]string{
		"ZRCIN_API":                 "http://127.0.0.1:8080",
		"ZRCIN_PROJECT":             "p-1",
		"ZRCIN_SELF":                "in-9",
		"ZRCIN_AGENT":               "http://127.0.0.1:8080/agent/p-1/api",
		"ZRCIN_BATTLE":              "http://127.0.0.1:8080/p/p-1/api",
		"ZRCIN_AUTH":                "Authorization: Bearer tok-xyz",
		aop.DisableNativeIOASendEnv: "1",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
	if env["ZRCIN_BIN"] == "" {
		t.Fatal("ZRCIN_BIN is empty; the shell cannot call the native read verbs")
	}
}

func TestSessionToolEnvIsNilWithoutIdentity(t *testing.T) {
	// A case with no identity for this turn must yield nil, not an environment
	// with empty values the model could mistake for real ones.
	if env := sessionToolEnv(t.TempDir(), "", 0); env != nil {
		t.Fatalf("missing identity produced %+v", env)
	}
	if env := sessionToolEnv("", "", 0); env != nil {
		t.Fatalf("empty dir produced %+v", env)
	}
}

// A tunnel node's sessions call the zrcin binary the launcher installed, not
// this process's executable (docs/14 §8 Z4).
func TestSessionToolEnvUsesTheNodeCLIPath(t *testing.T) {
	dir := t.TempDir()
	zrcinDir := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zrcinDir, "identity.json"), []byte(`{"api_base":"http://127.0.0.1:8080","project_id":"p1","intent_id":"i1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := sessionToolEnv(dir, "/opt/zrcin-node/bin/zrcin", 0)
	if got := env["ZRCIN_BIN"]; got != "/opt/zrcin-node/bin/zrcin" {
		t.Fatalf("ZRCIN_BIN = %q, want the node-local binary", got)
	}
}

// A tunnel node reaches the console on its own loopback mirror, on a port the
// launcher allocated: every URL the shell reads names that port, not the
// platform's (docs/14 §7.2).
func TestSessionToolEnvUsesTheConsoleMirrorPort(t *testing.T) {
	dir := t.TempDir()
	zrcinDir := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zrcinDir, "identity.json"),
		[]byte(`{"api_base":"http://127.0.0.1:8080","project_id":"p1","intent_id":"i1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := sessionToolEnv(dir, "", 28080)
	for key, want := range map[string]string{
		"ZRCIN_API":    "http://127.0.0.1:28080",
		"ZRCIN_AGENT":  "http://127.0.0.1:28080/agent/p1/api",
		"ZRCIN_BATTLE": "http://127.0.0.1:28080/p/p1/api",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
}

func TestReplacePort(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080":   "http://127.0.0.1:28080",
		"https://ops.example.com": "https://ops.example.com:28080",
		"https://[::1]:8443":      "https://[::1]:28080",
		"not a url":               "not a url",
	}
	for in, want := range cases {
		if got := replacePort(in, 28080); got != want {
			t.Errorf("replacePort(%q) = %q, want %q", in, got, want)
		}
	}
}
