// JWT session tokens. HS256 with a per-instance secret (kept in the data
// directory), 12-hour expiry, no refresh, no server-side session table: the
// token is self-contained and a secret rotation invalidates every one of them
// at once. The method is pinned via WithValidMethods so a token that claims
// "alg: none" (or any other algorithm) is rejected, not parsed.
package account

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultTTL matches the console session lifetime the cookie used to carry.
const DefaultTTL = 12 * time.Hour

// Signer mints and verifies the console's session JWTs. One instance, one
// key: the same secret must sign and verify, so rotation is a restart away
// and every outstanding session dies with the old key.
type Signer struct {
	key []byte
	ttl time.Duration
}

// Claims is the session a token names: the users-table row and the display
// name. Username is a convenience for the console's greeting, not an
// authorization decision.
type Claims struct {
	UID      int64
	Username string
}

// sessionClaims is the wire form; the sub claim carries the row id.
type sessionClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// NewSigner builds a signer. A too-short secret is refused here rather than
// failing later at the gate: a signer that cannot prove anything must not
// exist.
func NewSigner(secret []byte, ttl time.Duration) (*Signer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt secret too short: %d bytes (want >= 32)", len(secret))
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Signer{key: secret, ttl: ttl}, nil
}

// TTL reports the session lifetime, for cookies and expiry display.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Sign mints a token for u. The returned time is the absolute expiry, so the
// caller can show it and stamp Max-Age without re-deriving it.
func (s *Signer) Sign(u *User) (token string, expires time.Time, err error) {
	now := time.Now()
	expires = now.Add(s.ttl)
	claims := sessionClaims{
		Username: u.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(u.ID, 10),
			Issuer:    "4zrcin",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign session: %w", err)
	}
	return signed, expires, nil
}

// Parse verifies a token's signature, algorithm and expiry and returns the
// session it names. Any failure — bad signature, wrong algorithm, missing or
// past expiry — is the caller's "not authenticated".
func (s *Signer) Parse(token string) (Claims, error) {
	t, err := jwt.ParseWithClaims(token, &sessionClaims{}, func(t *jwt.Token) (any, error) {
		return s.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("4zrcin"),
		jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return Claims{}, ErrAuthFailed
	}
	claims, ok := t.Claims.(*sessionClaims)
	if !ok {
		return Claims{}, ErrAuthFailed
	}
	uid, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Claims{}, ErrAuthFailed
	}
	return Claims{UID: uid, Username: claims.Username}, nil
}
