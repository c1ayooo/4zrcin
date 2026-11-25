package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The startup line names the file, never the secret. instanceSecretNotice is
// the only path from a freshly generated signing key to stdout, and it does
// not receive the value at all — this test pins the shape of what it prints.
func TestInstanceSecretNoticeCarriesPathNotSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jwt.key")
	value := strings.Repeat("9e0f", 16) // 64 hex chars, shaped like the secret
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notice := instanceSecretNotice(path)
	if !strings.Contains(notice, path) {
		t.Fatalf("notice does not say where the secret lives: %q", notice)
	}
	if strings.Contains(notice, value) {
		t.Fatalf("notice leaks the secret value: %q", notice)
	}
}

// ensureInstanceSecret persists the signing key: a restart must read the same
// key or every session dies and the derived Hub credential breaks. A legacy
// console.token (the old operator token's file) is adopted as entropy so the
// Hub credential stays stable across the upgrade.
func TestInstanceSecretIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	secret, path, generated, err := ensureInstanceSecret(dir)
	if err != nil || !generated {
		t.Fatalf("first start: generated=%t err=%v", generated, err)
	}
	if len(secret) < 32 || path != filepath.Join(dir, "jwt.key") {
		t.Fatalf("first start: secret=%d bytes path=%q", len(secret), path)
	}
	again, _, generatedAgain, err := ensureInstanceSecret(dir)
	if err != nil || generatedAgain {
		t.Fatalf("restart: generated=%t err=%v", generatedAgain, err)
	}
	if again != secret {
		t.Fatal("restart regenerated the signing secret")
	}

	// A fresh instance with only a legacy console.token adopts it as entropy.
	legacyDir := t.TempDir()
	legacy := filepath.Join(legacyDir, "console.token")
	old := strings.Repeat("ab12", 16) + "\n"
	if err := os.WriteFile(legacy, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	adopted, _, generatedLegacy, err := ensureInstanceSecret(legacyDir)
	if err != nil || generatedLegacy {
		t.Fatalf("legacy adoption: generated=%t err=%v", generatedLegacy, err)
	}
	if adopted != strings.TrimSpace(old) {
		t.Fatal("legacy console.token was not adopted as the secret")
	}
}
