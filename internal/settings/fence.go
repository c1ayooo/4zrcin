package settings

// The instance fence (spec #1 工单3): command patterns that never execute and
// domains that are never reached, on every project and every node the
// instance drives. It lives in settings.json beside the runtime knobs — it is
// the operator's own red lines, not harness configuration — and it rides
// every turn as the 4zrcin.GlobalFence fork extension, so a save takes effect
// on the next round without a restart, on remote nodes included.
//
// The lists are deny-lists over the project fence: a global hit wins before
// the project's own rows are consulted. Switches are honest states: a
// disabled list keeps its rows but judges nothing, and flipping it back on
// needs no retype.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/project"
)

// Fence bounds and the closed action set. Beyond the row caps the operator is
// maintaining a blacklist instead of a red line, which the deny-list semantics
// cannot make readable.
const (
	MaxFenceCommands = 64
	MaxFenceDomains  = 128

	MaxFencePattern = 200
	MaxFenceDesc    = 200

	FenceActionDeny   = "deny"
	FenceActionReview = "review"
)

var fenceActions = map[string]bool{FenceActionDeny: true, FenceActionReview: true}

// GlobalCommandRule is one never-execute (or always-ask) command pattern.
type GlobalCommandRule struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Desc    string `json:"desc,omitempty"`
	Action  string `json:"action"`
}

// GlobalDomainRule is one never-reach identity: a bare domain (covering the
// domain and its whole subdomain tree) or a CIDR block — the same shapes and
// canonical form as the project fence's host column (the node-side matcher is
// literally shared).
type GlobalDomainRule struct {
	ID   string `json:"id"`
	Host string `json:"host"`
}

// Fence is the instance fence as stored and as sent on the wire.
type Fence struct {
	EnabledCommands bool                `json:"enabled_commands,omitempty"`
	Commands        []GlobalCommandRule `json:"commands,omitempty"`
	EnabledDomains  bool                `json:"enabled_domains,omitempty"`
	Domains         []GlobalDomainRule  `json:"domains,omitempty"`
}

// IsEmpty reports whether the fence holds nothing: the per-turn extension
// sends nothing for an empty fence, keeping the wire byte-identical.
func (f Fence) IsEmpty() bool {
	return len(f.Commands) == 0 && len(f.Domains) == 0
}

// NormalizeFence validates and fills defaults. Validation runs on every save
// regardless of the switches (the same unconditional rule the project fence
// applies), so a stored draft can never hold a row the node would reject the
// moment the operator flips the switch. Row ids are stable: an operator id is
// kept verbatim (charset-checked), a new row receives a fresh non-positional
// one, and ids are unique across both lists.
func NormalizeFence(in Fence) (Fence, error) {
	out := Fence{EnabledCommands: in.EnabledCommands, EnabledDomains: in.EnabledDomains}
	ids := map[string]bool{}
	if len(in.Commands) > MaxFenceCommands {
		return Fence{}, fmt.Errorf("命令禁令最多 %d 条（收到 %d 条）", MaxFenceCommands, len(in.Commands))
	}
	for i, rule := range in.Commands {
		rule.Pattern = strings.TrimSpace(rule.Pattern)
		rule.Desc = strings.TrimSpace(rule.Desc)
		if rule.Pattern == "" || len(rule.Pattern) > MaxFencePattern {
			return Fence{}, fmt.Errorf("命令禁令 %d 的 pattern 必填且不超过 %d 字节", i+1, MaxFencePattern)
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return Fence{}, fmt.Errorf("命令禁令 %d 的 pattern 无法编译：%w", i+1, err)
		}
		if rule.Desc != "" && len(rule.Desc) > MaxFenceDesc {
			return Fence{}, fmt.Errorf("命令禁令 %d 的 desc 超过 %d 字节", i+1, MaxFenceDesc)
		}
		if rule.Action == "" {
			rule.Action = FenceActionDeny
		}
		if !fenceActions[rule.Action] {
			return Fence{}, fmt.Errorf("命令禁令 %d 的 action 只能是 deny 或 review（收到 %q）", i+1, rule.Action)
		}
		var err error
		if rule.ID, err = fenceID(rule.ID, ids); err != nil {
			return Fence{}, fmt.Errorf("命令禁令 %d：%w", i+1, err)
		}
		out.Commands = append(out.Commands, rule)
	}
	if len(in.Domains) > MaxFenceDomains {
		return Fence{}, fmt.Errorf("域名禁令最多 %d 条（收到 %d 条）", MaxFenceDomains, len(in.Domains))
	}
	for i, rule := range in.Domains {
		host, err := project.NormalizeFenceHost(rule.Host)
		if err != nil {
			return Fence{}, fmt.Errorf("域名禁令 %d 的 host %w", i+1, err)
		}
		rule.Host = host
		var err2 error
		if rule.ID, err2 = fenceID(rule.ID, ids); err2 != nil {
			return Fence{}, fmt.Errorf("域名禁令 %d：%w", i+1, err2)
		}
		out.Domains = append(out.Domains, rule)
	}
	return out, nil
}

// fenceID validates an operator-provided id or generates a fresh
// non-positional one. Ids are unique across both lists: the audit trail names
// a row by id alone.
func fenceID(id string, taken map[string]bool) (string, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		if len(id) > 64 {
			return "", fmt.Errorf("id 超过 64 字符")
		}
		for _, r := range id {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			default:
				return "", fmt.Errorf("id 含非法字符：%s", id)
			}
		}
		if taken[id] {
			return "", fmt.Errorf("id 重复：%s", id)
		}
		taken[id] = true
		return id, nil
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("id 熵不可用：%w", err)
	}
	generated := "gf-" + hex.EncodeToString(b[:])
	for taken[generated] {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		generated = "gf-" + hex.EncodeToString(b[:])
	}
	taken[generated] = true
	return generated, nil
}

// FenceOf returns the stored instance fence. An unset fence is the zero value:
// nothing fenced, nothing on the wire.
func (s *Store) FenceOf() Fence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneFence(s.f.Fence)
}

// cloneFence copies both row slices. A shallow read handed out a slice that
// shared its backing array with the stored fence, and encoding/json reuses
// slice elements when it decodes into a non-empty slice: a *rejected* PUT
// (an uncompilable pattern, say) could then mutate rows it never saved. The
// read is now independent of the store.
func cloneFence(f Fence) Fence {
	if f.Commands != nil {
		f.Commands = append([]GlobalCommandRule(nil), f.Commands...)
	}
	if f.Domains != nil {
		f.Domains = append([]GlobalDomainRule(nil), f.Domains...)
	}
	return f
}

// SetFence validates and persists the instance fence to settings.json,
// preserving the model and runtime blocks (writeMu is shared because both
// rewrite the same legacy file). The stored shape is the normalized one, so
// memory, disk and the next turn's extension payload agree.
func (s *Store) SetFence(fence Fence) error {
	normalized, err := NormalizeFence(fence)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	f := s.f
	s.mu.RUnlock()
	f.Fence = normalized
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.mu.Lock()
	s.f = f
	s.mu.Unlock()
	return nil
}
