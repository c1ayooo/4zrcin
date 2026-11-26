package istate

import (
	"crypto/rand"
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSchema(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "istate_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])[:10])
}

// The kv modes must agree: put then get on both dialects, a missing key reads
// as absent, and a put overwrites.
func TestKVRoundTrip(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		kv, err := OpenKVFile(filepath.Join(t.TempDir(), "state"))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		if _, ok, err := kv.Get("missing.json"); err != nil || ok {
			t.Fatalf("missing key: ok=%v err=%v", ok, err)
		}
		if err := kv.Put("doc.json", []byte(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := kv.Get("doc.json")
		if err != nil || !ok || string(raw) != `{"a":1}` {
			t.Fatalf("get: ok=%v raw=%q err=%v", ok, raw, err)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("需要 ZRCIN_TEST_PG_DSN")
		}
		kv, err := OpenKVPGSchema(dsn, testSchema(t))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		if _, ok, err := kv.Get("missing.json"); err != nil || ok {
			t.Fatalf("missing key: ok=%v err=%v", ok, err)
		}
		if err := kv.Put("doc.json", []byte(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
		if err := kv.Put("doc.json", []byte(`{"a":2}`)); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := kv.Get("doc.json")
		if err != nil || !ok || string(raw) != `{"a":2}` {
			t.Fatalf("get: ok=%v raw=%q err=%v", ok, raw, err)
		}
	})
}
