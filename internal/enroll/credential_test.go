package enroll

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	testNodeID   = "0123456789abcdef0123456789abcdef"
	testIssueKey = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
)

// An enrollment token is "zr1_" plus 32 random bytes in unpadded base64url,
// and what the platform keeps for it is the SHA-256 of the whole string. The
// plaintext exists only in the creation response.
func TestEnrollmentTokenShapeAndHash(t *testing.T) {
	tok, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "zr1_") || len(tok) != len("zr1_")+43 {
		t.Fatalf("token shape = %q (len %d), want zr1_ + 43 base64url chars", tok, len(tok))
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(tok, "zr1_"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("token body is not 32 bytes: len=%d err=%v", len(raw), err)
	}
	sum := HashCredential(tok)
	if !hex64.MatchString(sum) {
		t.Fatalf("HashCredential = %q, want 64 lower-case hex chars", sum)
	}
	if strings.Contains(sum, strings.TrimPrefix(tok, "zr1_")) {
		t.Fatal("the hash exposes the token body")
	}
	// Independent source of truth for the digest: the published SHA-256 test
	// vector for "abc", so a digest that is right by construction cannot pass.
	const abcSHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := HashCredential("abc"); got != abcSHA256 {
		t.Fatalf("HashCredential(abc) = %s, want %s", got, abcSHA256)
	}
}

// Two tokens never collide.
func TestEnrollmentTokensAreIndependent(t *testing.T) {
	a, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || HashCredential(a) == HashCredential(b) {
		t.Fatalf("two tokens are identical: %q", a)
	}
}

// A node credential is "zn1_<node id>_<32 bytes base64url>". It is derived,
// not drawn: the same issue key, node id and install id give the same string,
// so an install retry can be handed the same credential although the file
// only ever holds its hash.
func TestNodeCredentialIsDerivedAndShaped(t *testing.T) {
	key := []byte(testIssueKey)
	cred := NodeCredential(key, testNodeID, "host-a")
	if !strings.HasPrefix(cred, "zn1_"+testNodeID+"_") || len(cred) != len("zn1_")+32+1+43 {
		t.Fatalf("credential shape = %q", cred)
	}
	if again := NodeCredential(key, testNodeID, "host-a"); again != cred {
		t.Fatal("the same inputs gave two different credentials")
	}
	if other := NodeCredential(key, testNodeID, "host-b"); other == cred {
		t.Fatal("a different install id gave the same credential")
	}
	if other := NodeCredential(key, "ffffffffffffffffffffffffffffffff", "host-a"); other == cred {
		t.Fatal("a different node id gave the same credential")
	}
	if other := NodeCredential(bytes.Repeat([]byte("K"), 32), testNodeID, "host-a"); other == cred {
		t.Fatal("a different issue key gave the same credential")
	}
}

func TestParseCredential(t *testing.T) {
	tok, err := NewEnrollmentToken()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseCredential(tok)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if got.Kind != CredentialEnrollmentToken || got.SHA256 != HashCredential(tok) || got.NodeID != "" {
		t.Fatalf("token parsed to %+v", got)
	}

	cred := NodeCredential([]byte(testIssueKey), testNodeID, "host-a")
	got, err = ParseCredential(cred)
	if err != nil {
		t.Fatalf("parse node credential: %v", err)
	}
	if got.Kind != CredentialNode || got.NodeID != testNodeID || got.SHA256 != HashCredential(cred) {
		t.Fatalf("node credential parsed to %+v", got)
	}

	// Anything that is not one of the two shapes is rejected before it is
	// hashed or looked up: a credential of this platform is always well formed.
	for _, bad := range []string{
		"",
		"Bearer abc",
		"zr1_",
		"zr1_short",
		"zr1_" + strings.Repeat("A", 44),
		strings.ToUpper(tok),
		tok + "\n",
		"zn1_" + testNodeID,
		"zn1_NOTHEX_NOTHEX_NOTHEX_NOTHEX_NOTHEX_" + strings.TrimPrefix(tok, "zr1_"),
		"zn1_" + testNodeID + "_short",
		"zk1_" + strings.TrimPrefix(tok, "zr1_"),
	} {
		if _, err := ParseCredential(bad); err == nil {
			t.Errorf("ParseCredential(%q) accepted a malformed credential", bad)
		}
	}
}
