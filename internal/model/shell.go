package model

import "time"

// Shell types name the language of the deployed script.
const (
	ShellTypePHP  = "php"
	ShellTypeJSP  = "jsp"
	ShellTypeASPX = "aspx"
)

// Shell protocols. v2 is the encrypted envelope: the communication key is
// baked into the deployed file and never appears on the wire as itself; a
// random header or form field carries each request. legacy is the plaintext
// one-liner with a connect password.
const (
	ShellProtocolV2     = "v2"
	ShellProtocolLegacy = "legacy"
)

// Shell statuses are the closed set a row's liveness may hold. removed /
// residual / remove_failed are terminal outcomes of a removal attempt, not
// liveness: residual means a legacy shell cannot delete itself and is still on
// the target.
const (
	ShellStatusUnknown      = "unknown"
	ShellStatusAlive        = "alive"
	ShellStatusDead         = "dead"
	ShellStatusRemoved      = "removed"
	ShellStatusResidual     = "residual"
	ShellStatusRemoveFailed = "remove_failed"
)

// Shell sources record who registered the row: the operator at the console, or
// the model through a harness tool.
const (
	ShellSourceOperator = "operator"
	ShellSourceModel    = "model"
)

// MaxShellOutput bounds the command output a row keeps as its last result. A
// shell is a ledger row, not an evidence store: callers cut with
// webshell.TruncateOutput before a write.
const MaxShellOutput = 8 << 10

// Shell is one managed webshell in the project ledger. Identity is the URL:
// one address serves one shell, so a second live row for the same URL is a
// mistake the store refuses instead of a second shell.
//
// Secret is the sealed credential (a v2 communication key, or a legacy connect
// password) — ciphertext only, the plaintext never lives in the ledger.
// NodeID is the vantage the shell is operated from: traffic to the target
// leaves that node, not the console.
type Shell struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Type          string     `json:"type"`
	Protocol      string     `json:"protocol"`
	Secret        string     `json:"secret,omitempty"`
	Carrier       string     `json:"carrier,omitempty"`
	PayloadSHA256 string     `json:"payload_sha256,omitempty"`
	NodeID        string     `json:"node_id,omitempty"`
	Target        string     `json:"target,omitempty"`
	Source        string     `json:"source"`
	RunID         string     `json:"run_id,omitempty"`
	Status        string     `json:"status"`
	Note          string     `json:"note,omitempty"`
	OSKind        string     `json:"os_kind,omitempty"`
	LastChecked   *time.Time `json:"last_checked,omitempty"`
	LastOutput    string     `json:"last_output,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// IsShellType reports whether t names a shell type.
func IsShellType(t string) bool {
	switch t {
	case ShellTypePHP, ShellTypeJSP, ShellTypeASPX:
		return true
	}
	return false
}

// IsShellProtocol reports whether p names a shell protocol.
func IsShellProtocol(p string) bool {
	switch p {
	case ShellProtocolV2, ShellProtocolLegacy:
		return true
	}
	return false
}

// IsShellStatus reports whether s names a shell status.
func IsShellStatus(s string) bool {
	switch s {
	case ShellStatusUnknown, ShellStatusAlive, ShellStatusDead,
		ShellStatusRemoved, ShellStatusResidual, ShellStatusRemoveFailed:
		return true
	}
	return false
}
