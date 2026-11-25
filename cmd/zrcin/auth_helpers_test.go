package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/account"
)

// testJWTSecret is the fixed instance signing secret the auth tests share.
// A real instance generates its own (ensureInstanceSecret); the tests only
// need one stable key so minted operator tokens verify at the gate.
var testJWTSecret = []byte("test-instance-signing-secret-0123456789abcdef")

// testAuth builds the gate the way the tests need it: accounts may be nil
// (the login/register endpoints then answer 500, which those tests never
// touch), and sessions nil means no harness credential is accepted.
func testAuth(accounts *account.Store, sessions sessionResolver) *consoleAuth {
	return newConsoleAuth(accounts, testJWTSecret, sessions)
}

// testAccountStore opens a throwaway account store. Callers Create their own
// account when a test must exercise the login path.
func testAccountStore(t *testing.T) *account.Store {
	t.Helper()
	s, err := account.Open(filepath.Join(t.TempDir(), "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// operatorJWT mints a valid operator session token, the successor of the old
// shared console token in these tests.
func operatorJWT(t *testing.T, username string) string {
	t.Helper()
	signer, err := account.NewSigner(testJWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := signer.Sign(&account.User{ID: 1, Username: username})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
