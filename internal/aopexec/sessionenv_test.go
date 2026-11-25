package aopexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/aop"
)

func TestSessionToolEnvMatchesCaseState(t *testing.T) {
	dir := t.TempDir()
	tw := filepath.Join(dir, ".tw")
	if err := os.MkdirAll(tw, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tw, "identity.json"), []byte(
		`{"api_base":"http://127.0.0.1:8080/","project_id":"p-1","intent_id":"in-9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tw, "auth"),
		[]byte("Authorization: Bearer tok-xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := sessionToolEnv(dir)
	want := map[string]string{
		"TW_API":                    "http://127.0.0.1:8080",
		"TW_PROJECT":                "p-1",
		"TW_SELF":                   "in-9",
		"TW_AGENT":                  "http://127.0.0.1:8080/agent/p-1/api",
		"TW_BATTLE":                 "http://127.0.0.1:8080/p/p-1/api",
		"TW_AUTH":                   "Authorization: Bearer tok-xyz",
		aop.DisableNativeIOASendEnv: "1",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
	if env["TW_BIN"] == "" {
		t.Fatal("TW_BIN is empty; the shell cannot call the native read verbs")
	}
}

func TestSessionToolEnvIsNilWithoutIdentity(t *testing.T) {
	// A case with no identity for this turn must yield nil, not an environment
	// with empty values the model could mistake for real ones.
	if env := sessionToolEnv(t.TempDir()); env != nil {
		t.Fatalf("missing identity produced %+v", env)
	}
	if env := sessionToolEnv(""); env != nil {
		t.Fatalf("empty dir produced %+v", env)
	}
}
