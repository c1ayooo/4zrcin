// Package enroll is the node ingress of a public platform (docs/15 §3): the
// enrollment records an operator hands out, the profiles of the nodes that
// joined with them, and the check of the two credentials those produce.
//
// It is the record-keeping core only. The tunnel (internal/tunnel), the
// artifacts and the launcher build on what this package exposes and never
// edit its file:
//
//   - Store is the single writer of <data>/remote-nodes.json. Credentials are
//     stored as SHA-256; the plaintext of an enrollment token exists only in
//     the response that created it.
//   - Ingress is the public routing shell behind /join/, /node/ and /tunnel/.
//     Later tickets add routes to it; they do not touch the console gate.
//
// The package must not import internal/api: the node views there read these
// records, so the dependency only runs in that direction.
package enroll

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Credential prefixes. Both are fixed so a bearer can be told apart by shape
// before anything is hashed or looked up, and so one kind can never be
// presented where the other is required.
const (
	tokenPrefix      = "zr1_"
	credentialPrefix = "zn1_"

	// secretBytes is the entropy of the random part of both credentials.
	secretBytes = 32
)

// CredentialKind is which of the two secrets a caller presented.
type CredentialKind int

const (
	// CredentialEnrollmentToken is the one-time token of an enrollment record
	// ("zr1_<32 bytes base64url>"): it admits one host and is spent by it.
	CredentialEnrollmentToken CredentialKind = iota + 1
	// CredentialNode is a node's long-lived credential
	// ("zn1_<node id>_<32 bytes base64url>").
	CredentialNode
)

var (
	// ErrCredential is a malformed credential: not one of the two shapes. The
	// caller gets the same 401 as for a well-formed secret nobody issued.
	ErrCredential = errors.New("凭据格式无效")

	nodeIDRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	base64_32 = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// NewEnrollmentToken draws a fresh enrollment token.
func NewEnrollmentToken() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成接入令牌：%w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashCredential is the only form in which a credential is kept: the SHA-256
// of the whole string, as lower-case hex. It is not a password hash on
// purpose: both credentials carry 256 bits of random entropy (the node
// credential through an HMAC keyed by the instance), so there is nothing to
// grind offline and a slow hash would only cost the platform.
func HashCredential(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// NodeCredential derives the credential of a node from the instance's issue
// key, the node id and the install id.
//
// It is derived rather than drawn because "the same token and the same install
// id return the same credential" (docs/15 §3.3) has to survive a platform
// restart, and the file keeps hashes only: a drawn secret would be
// unrecoverable after one. HMAC is one-way, so the credential tells nothing
// about the issue key, and it is keyed by the install id, so it cannot be
// recomputed from a leaked enrollment token once that token is spent.
func NodeCredential(issueKey []byte, nodeID, installID string) string {
	mac := hmac.New(sha256.New, issueKey)
	_, _ = mac.Write([]byte("4zrcin/node-credential/v1\x00" + nodeID + "\x00" + installID))
	return credentialPrefix + nodeID + "_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Credential is a presented secret after its shape was accepted.
type Credential struct {
	Kind   CredentialKind
	SHA256 string
	// NodeID is the node the credential names; empty for an enrollment token.
	NodeID string
}

// ParseCredential checks that secret is one of the two shapes this platform
// issues and returns its digest. Shape first: a platform credential is always
// well formed, so anything else is refused before it is hashed.
func ParseCredential(secret string) (Credential, error) {
	switch {
	case strings.HasPrefix(secret, tokenPrefix):
		body := strings.TrimPrefix(secret, tokenPrefix)
		if !base64_32.MatchString(body) {
			return Credential{}, ErrCredential
		}
		return Credential{Kind: CredentialEnrollmentToken, SHA256: HashCredential(secret)}, nil
	case strings.HasPrefix(secret, credentialPrefix):
		rest := strings.TrimPrefix(secret, credentialPrefix)
		nodeID, sum, ok := strings.Cut(rest, "_")
		if !ok || !nodeIDRe.MatchString(nodeID) || !base64_32.MatchString(sum) {
			return Credential{}, ErrCredential
		}
		return Credential{Kind: CredentialNode, NodeID: nodeID, SHA256: HashCredential(secret)}, nil
	}
	return Credential{}, ErrCredential
}
