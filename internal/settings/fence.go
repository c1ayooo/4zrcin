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
	// Desc labels the row on the fence page and in the audit ("DNS 基础设
	// 施", "被动情报源"). The harness ignores it — only the ID and host
	// shape reach the node-side matcher.
	Desc string `json:"desc,omitempty"`
}

// Fence is the instance fence as stored and as sent on the wire.
type Fence struct {
	EnabledCommands bool                `json:"enabled_commands,omitempty"`
	Commands        []GlobalCommandRule `json:"commands,omitempty"`
	EnabledDomains  bool                `json:"enabled_domains,omitempty"`
	Domains         []GlobalDomainRule  `json:"domains,omitempty"`
	// EnabledAllowDomains gates the default-allow list (fork patch 37):
	// infrastructure names (DNS/DoH endpoints, passive intel sources) the
	// executor may always reach, on every project. The deny lists still win
	// when both name a host.
	EnabledAllowDomains bool               `json:"enabled_allow_domains,omitempty"`
	AllowDomains        []GlobalDomainRule `json:"allow_domains,omitempty"`
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
	out := Fence{EnabledCommands: in.EnabledCommands, EnabledDomains: in.EnabledDomains, EnabledAllowDomains: in.EnabledAllowDomains}
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
	for i, rule := range in.AllowDomains {
		host, err := project.NormalizeFenceHost(rule.Host)
		if err != nil {
			return Fence{}, fmt.Errorf("放行域名 %d 的 host %w", i+1, err)
		}
		id, err := fenceID(rule.ID, ids)
		if err != nil {
			return Fence{}, fmt.Errorf("放行域名 %d：%w", i+1, err)
		}
		out.AllowDomains = append(out.AllowDomains, GlobalDomainRule{ID: id, Host: host, Desc: rule.Desc})
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

// PresetAllowDomains is the infrastructure allowlist the fence page loads in
// one click: DNS/DoH resolution endpoints and passive intel sources the
// executor legitimately reaches on every engagement. Rows are ordinary
// operator rows after loading — editable, removable, switchable off. The
// deny list outranks them when both name a host.
func PresetAllowDomains() []GlobalDomainRule {
	return []GlobalDomainRule{
		{ID: "allow-dns-alidns", Host: "dns.alidns.com", Desc: "DNS 基础设施（阿里 DoH/DoT）"},
		{ID: "allow-dns-dohpub", Host: "doh.pub", Desc: "DNS 基础设施（腾讯 DoH）"},
		{ID: "allow-dns-2235", Host: "223.5.5.5", Desc: "DNS 基础设施（阿里公共解析）"},
		{ID: "allow-dns-8888", Host: "8.8.8.8", Desc: "DNS 基础设施（Google 公共解析）"},
		{ID: "allow-dns-114", Host: "114.114.114.114", Desc: "DNS 基础设施（114 公共解析）"},
		{ID: "allow-intel-crtsh", Host: "crt.sh", Desc: "被动情报源（证书透明度查询）"},
		{ID: "allow-intel-certspotter", Host: "api.certspotter.com", Desc: "被动情报源（证书监控）"},
	}
}

// ApplyPresetAllowDomains appends the preset rows the fence does not carry
// yet (idempotent by id and host), normalizes the result and enforces the
// domain caps across both lists.
func ApplyPresetAllowDomains(existing Fence) (Fence, int, error) {
	present := map[string]bool{}
	for _, r := range existing.AllowDomains {
		present[r.ID] = true
		present["host:"+r.Host] = true
	}
	out := existing
	out.AllowDomains = append([]GlobalDomainRule(nil), existing.AllowDomains...)
	added := 0
	for _, preset := range PresetAllowDomains() {
		if present[preset.ID] || present["host:"+preset.Host] {
			continue
		}
		out.AllowDomains = append(out.AllowDomains, preset)
		present[preset.ID] = true
		present["host:"+preset.Host] = true
		added++
	}
	if added == 0 {
		return existing, 0, nil
	}
	normalized, err := NormalizeFence(out)
	if err != nil {
		return Fence{}, 0, err
	}
	return normalized, added, nil
}

// PresetCommands is the download / delete / upload command corpus the fence
// page loads in one click. Every row is review — the operator answers each
// live invocation, and after loading a row is an ordinary operator row:
// editable, removable, switchable off. Patterns match the command text a
// command-like tool call carries (the bash command, not a document quoting
// one), so these are good defaults, not a safety boundary.
func PresetCommands() []GlobalCommandRule {
	return []GlobalCommandRule{
		{ID: "preset-dl-wget", Pattern: `\bwget\b`, Desc: "下载文件（wget）", Action: FenceActionReview},
		{ID: "preset-dl-aria2", Pattern: `\baria2c\b`, Desc: "下载文件（aria2）", Action: FenceActionReview},
		{ID: "preset-dl-git-clone", Pattern: `\bgit\s+clone\b`, Desc: "克隆仓库（git clone）", Action: FenceActionReview},
		{ID: "preset-dl-pkg-install", Pattern: `\b(pip3?\s+install|npm\s+install|yarn\s+(add|install)|pnpm\s+(add|install)|go\s+install|cargo\s+install)\b`, Desc: "包管理器安装/下载", Action: FenceActionReview},
		{ID: "preset-dl-system-install", Pattern: `\b(apt(-get)?|dnf|yum|apk)\s+install\b`, Desc: "系统包安装", Action: FenceActionReview},
		{ID: "preset-dl-image-pull", Pattern: `\b(docker|podman)\s+(image\s+)?pull\b`, Desc: "拉取容器镜像", Action: FenceActionReview},
		{ID: "preset-dl-curl-output", Pattern: `\bcurl\b[^|;]*(\s-[a-zA-Z]*[oO]\b|\s--output\b|--remote-name\b)`, Desc: "curl 写文件下载", Action: FenceActionReview},
		{ID: "preset-del-rm-recursive", Pattern: `\brm\s+(-[a-zA-Z]*[rRfF][a-zA-Z]*\s+)+`, Desc: "递归/强制删除（rm -r/-f）", Action: FenceActionReview},
		{ID: "preset-del-shred", Pattern: `\b(shred|unlink)\b`, Desc: "粉碎/删除单个文件", Action: FenceActionReview},
		{ID: "preset-del-find-delete", Pattern: `\bfind\b[^|;]*-delete\b`, Desc: "find 批量删除", Action: FenceActionReview},
		{ID: "preset-del-git", Pattern: `\bgit\s+(clean\s+-[a-zA-Z]*[fF]|reset\s+--hard)\b`, Desc: "git 丢弃工作区（clean / reset --hard）", Action: FenceActionReview},
		{ID: "preset-del-sql", Pattern: `(?i)\b(drop\s+(table|database|schema)|truncate\s+table|delete\s+from)\b`, Desc: "SQL 删除/清空表", Action: FenceActionReview},
		{ID: "preset-up-transfer", Pattern: `\b(scp|sftp|rsync|ftp|lftp)\b`, Desc: "文件传输（上传/下载）", Action: FenceActionReview},
		{ID: "preset-up-curl", Pattern: `\bcurl\b[^|;]*(\s-[a-zA-Z]*T\s|\s--upload-file\b|-F\s+["']?[^"'\s]*=@)`, Desc: "curl 上传文件", Action: FenceActionReview},
	}
}

// ApplyPresetCommands appends the preset rows the fence does not carry yet: a
// row whose id OR pattern is already present is skipped, so loading is
// idempotent and an operator's edited row is never duplicated or overwritten.
// The result runs through the same normalization a hand save does (patterns
// compile-validated, cap enforced) — a fence too full for the remaining
// presets is an error, never a partial write.
func ApplyPresetCommands(existing Fence) (Fence, int, error) {
	present := map[string]bool{}
	for _, r := range existing.Commands {
		present[r.ID] = true
		present["pattern:"+r.Pattern] = true
	}
	out := existing
	out.Commands = append([]GlobalCommandRule(nil), existing.Commands...)
	added := 0
	for _, preset := range PresetCommands() {
		if present[preset.ID] || present["pattern:"+preset.Pattern] {
			continue
		}
		out.Commands = append(out.Commands, preset)
		present[preset.ID] = true
		present["pattern:"+preset.Pattern] = true
		added++
	}
	if added == 0 {
		return existing, 0, nil
	}
	normalized, err := NormalizeFence(out)
	if err != nil {
		return Fence{}, 0, err
	}
	return normalized, added, nil
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
	if f.AllowDomains != nil {
		f.AllowDomains = append([]GlobalDomainRule(nil), f.AllowDomains...)
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
