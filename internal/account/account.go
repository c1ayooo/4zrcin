// Package account is the instance-level operator account: one SQLite store
// holding the users table, bcrypt password hashing, and the JWT session tokens
// the console issues at login. It replaces the single operator token: there is
// no shared static credential any more, the gate accepts a signed JWT (bearer
// or cookie) minted by Sign after the password check.
//
// Registration is first-boot only: the store allows one account, and Create
// refuses once a row exists. There is no reset flow — the operator who loses
// the password deletes account.db and re-registers.
package account

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	// MinUsernameLen and MaxUsernameLen bound the login name; the regex below
	// is the whole rule.
	MinUsernameLen = 3
	MaxUsernameLen = 32
	// MinPasswordLen is the strength floor; MaxPasswordLen is bcrypt's input
	// limit in bytes — a longer password is a configuration error, not a
	// stronger one, and bcrypt would silently truncate it.
	MinPasswordLen = 8
	MaxPasswordLen = 72

	bcryptCost = bcrypt.DefaultCost
)

// usernameRule is the entire username grammar: letters, digits, underscore,
// hyphen, MinUsernameLen..MaxUsernameLen runes. Length is checked separately
// so an over-long name reports "too long" instead of a bare format error.
var usernameRule = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var (
	ErrUsernameTaken   = errors.New("username taken")
	ErrInvalidUsername = errors.New("invalid username")
	ErrWeakPassword    = errors.New("weak password")
	// ErrAuthFailed covers both "no such account" and "wrong password" at the
	// store level; the HTTP layer maps both to one response so the error never
	// says which one was true (username enumeration).
	ErrAuthFailed = errors.New("authentication failed")
)

// User is one row of the users table. PasswordHash is a bcrypt digest and is
// never rendered, logged or sent to a client.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    int64
	UpdatedAt    int64
}

// Store owns the account database. It keeps no idle connection: every call
// opens its work on the shared handle and releases it, like the ledger store.
type Store struct{ db *sql.DB }

// Open creates (or opens) the account database at path. The file has its own
// schema and its own user_version, deliberately separate from the project
// ledger: an account is instance state, and the ledger refuses every format
// change, so mixing them would couple one to the other.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteURL(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT    NOT NULL UNIQUE,
			password_hash TEXT    NOT NULL,
			created_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("account schema: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Has reports whether any account exists — the "registration closed" switch.
func (s *Store) Has() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Create validates the pair, hashes the password and inserts the row. It is
// the only writer, and the unique index is the concurrent-registration
// backstop: two races cannot both pass the Has() check.
func (s *Store) Create(username, password string) (*User, error) {
	username = trimUsername(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?,?,?,?)`,
		username, string(hash), now, now)
	if err != nil {
		// UNIQUE covers the race Has() cannot close; everything else is real.
		if isUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: now, UpdatedAt: now}, nil
}

// ByUsername returns the account, or ErrAuthFailed when it does not exist —
// the same error a wrong password produces one layer up, so no caller can
// accidentally distinguish the two.
func (s *Store) ByUsername(username string) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, password_hash, created_at, updated_at FROM users WHERE username = ?`,
		trimUsername(username))
	u := &User{}
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAuthFailed
		}
		return nil, err
	}
	return u, nil
}

// Verify is the login check. An unknown username costs the same bcrypt work as
// a known one (dummyCompare), so response timing cannot enumerate accounts;
// both failures return ErrAuthFailed.
func (s *Store) Verify(username, password string) (*User, error) {
	u, err := s.ByUsername(username)
	if err != nil {
		if errors.Is(err, ErrAuthFailed) {
			dummyCompare(password)
			return nil, ErrAuthFailed
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrAuthFailed
	}
	return u, nil
}

// ValidateUsername enforces the grammar and length of a login name.
func ValidateUsername(username string) error {
	n := len([]rune(username))
	if n < MinUsernameLen || n > MaxUsernameLen {
		return fmt.Errorf("%w: 需要 %d–%d 位", ErrInvalidUsername, MinUsernameLen, MaxUsernameLen)
	}
	if !usernameRule.MatchString(username) {
		return fmt.Errorf("%w: 仅允许字母、数字、下划线和连字符", ErrInvalidUsername)
	}
	return nil
}

// ValidatePassword enforces the strength floor. MaxPasswordLen is bcrypt's own
// input limit; rejecting above it beats silent truncation.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("%w: 至少 %d 位", ErrWeakPassword, MinPasswordLen)
	}
	if len(password) > MaxPasswordLen {
		return fmt.Errorf("%w: 至多 %d 字节", ErrWeakPassword, MaxPasswordLen)
	}
	var letter, digit bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'z' && !(r > 'Z' && r < 'a'):
			letter = true
		case r >= '0' && r <= '9':
			digit = true
		}
	}
	if !letter || !digit {
		return fmt.Errorf("%w: 需同时包含字母和数字", ErrWeakPassword)
	}
	return nil
}

func trimUsername(username string) string {
	start, end := 0, len(username)
	for start < end && isSpace(username[start]) {
		start++
	}
	for end > start && isSpace(username[end-1]) {
		end--
	}
	return username[start:end]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func isUniqueViolation(err error) bool {
	// modernc.org/sqlite reports the driver's constraint error text; matching
	// the message is the pragmatic check short of importing their error type.
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// dummyCompare burns the bcrypt work a real account would, for a username the
// store does not know. The hash is of an unrelated throwaway string; its value
// being public is irrelevant — it is compared against, never matched.
var (
	dummyOnce  sync.Once
	dummyHash  []byte
	dummyGuess = []byte("4zrcin-dummy-login-timing-equalizer")
)

func dummyCompare(password string) {
	dummyOnce.Do(func() {
		h, _ := bcrypt.GenerateFromPassword(dummyGuess, bcryptCost)
		dummyHash = h
	})
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

// sqliteURL builds the driver DSN: absolute, WAL, with a busy timeout so a
// concurrent login never trips over the schema write.
func sqliteURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	params := url.Values{}
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = params.Encode()
	return u.String()
}
