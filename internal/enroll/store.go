package enroll

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/c1ayooo/4zrcin/internal/noderes"
)

// Enrollment and Node states (docs/14 §7). States are explicit strings in
// the file so an operator can read it, but transitions go through the Store's
// methods only — a state that cannot be reached through a transition must not
// be expressible by editing around the store.
const (
	// Enrollment states.
	EnrollPending = "pending"
	EnrollJoined  = "joined"
	EnrollRevoked = "revoked"
	EnrollExpired = "expired"

	// Node states.
	NodeJoining    = "joining"
	NodeOnline     = "online"
	NodeOffline    = "offline"
	NodeDestroying = "destroying"
	NodeDestroyed  = "destroyed"
)

// Recipes and deploy methods of an enrollment (docs/14 §7.1).
const (
	RecipeFull    = "full"
	RecipeCapture = "capture"

	DeployCommand = "command"
	DeploySSH     = "ssh"
)

// DefaultTokenTTL is how long an enrollment token admits a host when the
// operator did not pick one.
const DefaultTokenTTL = 60 * time.Minute

// NodeNameRe is the operator-visible name shape (docs/14 §7): lowercase
// alphanumeric with inner dashes, 2 to 32 chars.
var NodeNameRe = nodePattern

// enrollment is one join invitation. TokenHash is the only trace of the
// secret; the plaintext exists in the create response alone.
type enrollment struct {
	ID            string    `json:"id"`
	TokenHash     string    `json:"token_hash"`
	Name          string    `json:"name"`
	Recipe        string    `json:"recipe"`
	Deploy        string    `json:"deploy"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	State         string    `json:"state"`
	NodeID        string    `json:"node_id,omitempty"`
	InstallID     string    `json:"install_id,omitempty"`
	JoinedAt      time.Time `json:"joined_at,omitempty"`
	RevokedReason string    `json:"revoked_reason,omitempty"`
}

// node is one joined host (docs/14 §7).
type node struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	EnrollmentID string     `json:"enrollment_id"`
	Recipe       string     `json:"recipe"`
	Deploy       string     `json:"deploy"`
	CredSHA256   string     `json:"credential_sha256"`
	State        string     `json:"state"`
	InstallID    string     `json:"install_id,omitempty"`
	Hostname     string     `json:"hostname,omitempty"`
	OS           string     `json:"os,omitempty"`
	Arch         string     `json:"arch,omitempty"`
	JoinedAt     time.Time  `json:"joined_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	DestroyReqAt *time.Time `json:"destroy_requested_at,omitempty"`
	DestroyedAt  *time.Time `json:"destroyed_at,omitempty"`
	// NodeBinPath is where the launcher reports the local zrcin binary; the
	// control plane puts it into $ZRCIN_BIN for sessions on this node
	// (docs/14 §8 Z4). Empty until the node reports it.
	NodeBinPath string `json:"node_bin_path,omitempty"`
	// The launcher's host sample and runtime facts (docs/14 §7.3): what the
	// node cards render. Absent until the first heartbeat after the upgrade.
	Resources       *noderes.Snapshot `json:"resources,omitempty"`
	LauncherVersion string            `json:"launcher_version,omitempty"`
	UptimeSeconds   int64             `json:"uptime_seconds,omitempty"`
	ScanRestarts    int               `json:"scan_restarts,omitempty"`
	// TunnelUp is the last heartbeat's tunnel claim; nil until reported.
	TunnelUp *bool `json:"tunnel_up,omitempty"`
	// The node's own loopback mirror ports, reported with every heartbeat
	// (docs/14 §7.2).
	MirrorHubPort     int `json:"mirror_hub_port,omitempty"`
	MirrorConsolePort int `json:"mirror_console_port,omitempty"`
	// ScanProxyDigest is the last reported fingerprint of the default egress
	// proxy the launcher reconciled scan to (empty for a launcher that
	// predates the report). ScanProxyApplied is derived from it against the
	// platform's current default, never persisted: it is recomputed at boot.
	ScanProxyDigest  string `json:"scan_proxy_digest,omitempty"`
	ScanProxyApplied bool   `json:"-"`
}

// file is the on-disk shape of <data>/remote-nodes.json. One file, atomic
// replace, credentials only as digests (docs/14 §7).
type file struct {
	Enrollments []enrollment `json:"enrollments"`
	Nodes       []node       `json:"nodes"`
}

// Store owns <data>/remote-nodes.json. All reads and writes go through the
// mutex; every mutation rewrites the whole file atomically, because the file
// is small (tens of records) and one writer keeps it honest.
type Store struct {
	path string
	now  func() time.Time

	mu struct {
		sync.RWMutex
		f file
	}
}

// Open loads the store from path, creating an empty one when the file does
// not exist yet. A file that exists but cannot be parsed is an error, never a
// silent reset: the operator has to decide what happened to their records.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取节点档案 %s：%w", path, err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s 不是合法的节点档案（不自动重置，请人工处理）：%w", path, err)
	}
	s.mu.f = f
	return s, nil
}

// saveLocked rewrites the file atomically: temp file in the same directory,
// fsync, rename. Callers hold the write lock.
func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.mu.f, "", "  ")
	if err != nil {
		return fmt.Errorf("编码节点档案：%w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建节点档案目录 %s：%w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".remote-nodes-*.tmp")
	if err != nil {
		return fmt.Errorf("写节点档案：%w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // no-op after a successful rename
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写节点档案：%w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("写节点档案：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写节点档案：%w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("写节点档案：%w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("写节点档案：%w", err)
	}
	return nil
}

// expiredLocked returns the effective state of an enrollment: a pending
// enrollment past its expiry reads as expired without rewriting the file —
// the stored state stays what the last transition left, and expiry is a
// fact about time, not an event.
func (e *enrollment) effectiveState(now time.Time) string {
	if e.State == EnrollPending && !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
		return EnrollExpired
	}
	return e.State
}
