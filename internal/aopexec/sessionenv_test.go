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

	env := sessionToolEnv(dir, "")
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
	if env := sessionToolEnv(t.TempDir(), ""); env != nil {
		t.Fatalf("missing identity produced %+v", env)
	}
	if env := sessionToolEnv("", ""); env != nil {
		t.Fatalf("empty dir produced %+v", env)
	}
}

// A tunnel node's sessions call the zrcin binary the launcher installed, not
// this process's executable (docs/15 §8 Z4).
func TestSessionToolEnvUsesTheNodeCLIPath(t *testing.T) {
	dir := t.TempDir()
	zrcinDir := filepath.Join(dir, ".zrcin")
	if err := os.MkdirAll(zrcinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zrcinDir, "identity.json"), []byte(`{"api_base":"http://127.0.0.1:8080","project_id":"p1","intent_id":"i1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := sessionToolEnv(dir, "/opt/zrcin-node/bin/zrcin")
	if got := env["ZRCIN_BIN"]; got != "/opt/zrcin-node/bin/zrcin" {
		t.Fatalf("ZRCIN_BIN = %q, want the node-local binary", got)
	}
}
