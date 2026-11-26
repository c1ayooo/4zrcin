package account

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"os"
	"strings"
	"testing"
)

// The PostgreSQL account store must behave like the file one: first account
// wins, the second registration is refused, login verifies, and a rename onto
// an existing name surfaces as ErrUsernameTaken.
func testSchema() string {
	// 10 random base32 characters via crypto/rand, mirroring the ledger ids.
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])[:10])
}

func TestPGAccountLifecycle(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN")
	}
	s, err := OpenPGSchema(dsn, "acct_"+testSchema())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if has, err := s.Has(); err != nil || has {
		t.Fatalf("fresh store Has=%v err=%v", has, err)
	}
	u, err := s.Create("operator", "s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("operator", "other-password2"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second registration = %v, want ErrUsernameTaken", err)
	}
	got, err := s.Verify("operator", "s3cret-password")
	if err != nil || got.ID != u.ID {
		t.Fatalf("verify: %v %+v", err, got)
	}
	if _, err := s.Verify("operator", "wrong-password"); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := s.UpdateUsername(u.ID, "operator"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("rename onto itself = %v, want ErrUsernameTaken", err)
	}
	if err := s.UpdatePassword(u.ID, "n3w-password1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify("operator", "n3w-password1"); err != nil {
		t.Fatal(err)
	}
}
