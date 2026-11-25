// Scope rules are the operator's machine-matchable statement of what this
// engagement may touch over the wire: which hosts, paths and methods are in
// range, and what to do with a request that matches nothing. They feed the
// traffic/command audit pipeline on the harness side; they never enter a brief,
// the verifier or a report — the prose boundary stays the scope document
// (docs/01-architecture.md §3). An absent or disabled rule set means "record
// only, change nothing": review-first suspension is an operator decision, not a
// platform default.
package project

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// The three actions a matching rule can take. record forwards and logs;
// review holds the request until the operator answers; block refuses it.
// review is the recommended default because a hold is reversible and a block
// is a pre-commitment the rule text cannot take back.
const (
	ScopeActionRecord = "record"
	ScopeActionReview = "review"
	ScopeActionBlock  = "block"
)

// ScopeRuleActions is the closed set, in operator-facing order.
var ScopeRuleActions = []string{ScopeActionRecord, ScopeActionReview, ScopeActionBlock}

// scopeRuleMethods is the closed method set a rule may name. Methods outside
// HTTP's common verbs cannot be matched by the proxy's rule engine, so
// accepting them would store a rule that silently never fires.
var scopeRuleMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

// ScopeRule is one match line: first hit wins, top to bottom.
type ScopeRule struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	// Method is "*" or one or more verbs joined by "|" (e.g. "POST|DELETE").
	Method string `json:"method"`
	// Host is "*", an exact hostname, a ".example.com" suffix form that
	// matches every subdomain, or a CIDR block (e.g. "10.0.0.0/8") matched
	// against the target's resolved address. No scheme, port or path — the
	// proxy matches on the authority only, and a port field would fork the
	// semantics.
	Host string `json:"host"`
	// PathPrefix is the literal URL path prefix, always starting with "/".
	PathPrefix string `json:"path_prefix"`
}

// ScopeRules is the project's audit range configuration. Enabled=false keeps
// the stored rules as a draft: the pipeline stays record-only, and flipping
// the switch back on does not require retyping the table.
type ScopeRules struct {
	Enabled       bool   `json:"enabled"`
	DefaultAction string `json:"default_action"`
	// Shadow judges every request and forwards it regardless of the verdict.
	// It is the trial mode for a new rule set: the journal shows what would
	// have been held without holding anything.
	Shadow bool        `json:"shadow,omitempty"`
	Rules  []ScopeRule `json:"rules,omitempty"`
}

// MaxScopeRules bounds the table. Beyond it the operator is maintaining a
// blacklist instead of a range, which the first-hit-wins semantics cannot make
// readable or auditable.
const MaxScopeRules = 64

// NormalizeScopeRules validates and normalizes an operator-submitted rule set.
// Validation is unconditional — even while Enabled is false — so the stored
// draft can never hold a rule the engine would reject at the moment the
// operator flips the switch. An invalid draft that validates only when
// enabled would be exactly the "looks configurable, does nothing" state the
// settings endpoints refuse elsewhere.
func NormalizeScopeRules(in ScopeRules) (ScopeRules, error) {
	out := ScopeRules{Enabled: in.Enabled, Shadow: in.Shadow}
	action := strings.ToLower(strings.TrimSpace(in.DefaultAction))
	if !validScopeAction(action) {
		return ScopeRules{}, fmt.Errorf("default_action 必须是 %s（收到 %q）", strings.Join(ScopeRuleActions, "/"), in.DefaultAction)
	}
	out.DefaultAction = action
	if len(in.Rules) > MaxScopeRules {
		return ScopeRules{}, fmt.Errorf("规则最多 %d 条（收到 %d 条）", MaxScopeRules, len(in.Rules))
	}
	ids := make(map[string]bool, len(in.Rules))
	seen := make(map[string]bool, len(in.Rules))
	rules := make([]ScopeRule, 0, len(in.Rules))
	for i, rule := range in.Rules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		if !validScopeAction(action) {
			return ScopeRules{}, fmt.Errorf("规则 %d 的 action 必须是 %s（收到 %q）", i+1, strings.Join(ScopeRuleActions, "/"), rule.Action)
		}
		method, err := normalizeScopeMethod(rule.Method)
		if err != nil {
			return ScopeRules{}, fmt.Errorf("规则 %d 的 method %w", i+1, err)
		}
		host, err := normalizeScopeHost(rule.Host)
		if err != nil {
			return ScopeRules{}, fmt.Errorf("规则 %d 的 host %w", i+1, err)
		}
		prefix, err := normalizeScopePathPrefix(rule.PathPrefix)
		if err != nil {
			return ScopeRules{}, fmt.Errorf("规则 %d 的 path_prefix %w", i+1, err)
		}
		id := strings.TrimSpace(rule.ID)
		if id == "" {
			// The console rows carry no id; the manifest is the only place one
			// is needed (journal events name the rule that fired).
			id = fmt.Sprintf("sr-%d", i+1)
		}
		if ids[id] {
			return ScopeRules{}, fmt.Errorf("规则 id 重复：%s", id)
		}
		ids[id] = true
		key := action + "\x00" + method + "\x00" + host + "\x00" + prefix
		if seen[key] {
			// A duplicate row can only ever fire as the first of the two;
			// keeping it would make the table lie about its own reach.
			return ScopeRules{}, errors.New("存在完全相同的重复规则")
		}
		seen[key] = true
		rules = append(rules, ScopeRule{ID: id, Action: action, Method: method, Host: host, PathPrefix: prefix})
	}
	out.Rules = rules
	return out, nil
}

func validScopeAction(action string) bool {
	for _, a := range ScopeRuleActions {
		if a == action {
			return true
		}
	}
	return false
}

func normalizeScopeMethod(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "*" {
		return "*", nil
	}
	if s == "" {
		return "", errors.New("必填（* 或 GET|POST 这类写法）")
	}
	tokens := strings.Split(s, "|")
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			return "", errors.New("不能有空的方法项")
		}
		if !scopeRuleMethods[token] {
			return "", fmt.Errorf("只能是 * 或 %s（收到 %q）", strings.Join(scopeRuleMethodNames(), "/"), token)
		}
		if seen[token] {
			return "", fmt.Errorf("方法重复：%s", token)
		}
		seen[token] = true
		out = append(out, token)
	}
	return strings.Join(out, "|"), nil
}

func scopeRuleMethodNames() []string {
	return []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
}

func normalizeScopeHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "*" {
		return "*", nil
	}
	if host == "" {
		return "", errors.New("必填（精确主机、.example.com 后缀形式、CIDR 网段或 *）")
	}
	// Fourth accepted form: a CIDR block (IPv4 or IPv6). The proxy matches it
	// against the target's resolved address, which is the only way to state
	// "this whole subnet is in range" — a hostname wildcard cannot express it.
	// The canonical spelling is whatever ParseCIDR accepts, lowercased.
	if strings.Contains(host, "/") {
		if _, _, err := net.ParseCIDR(host); err != nil {
			return "", fmt.Errorf("不是合法的 CIDR 网段（收到 %q）", raw)
		}
		return host, nil
	}
	if len(host) > 253 {
		return "", errors.New("超过了 253 个字符")
	}
	if strings.ContainsAny(host, ":/\\?#@ \t\r\n") {
		return "", errors.New("不能包含协议、端口、路径或空白；端口与路径不在范围规则里匹配")
	}
	// Two wildcard spellings mean the same thing and are normalized to the
	// leading "*." form: "*.example.com" and ".example.com" both match every
	// subdomain. The canonical form is what the console shows back and the
	// journal names, so both inputs must land on one of them.
	if strings.HasPrefix(host, ".") && len(host) > 1 {
		host = "*" + host
	}
	body := strings.TrimPrefix(host, "*.")
	if strings.Contains(body, "*") {
		return "", fmt.Errorf("通配只支持 *.example.com 的子域形式（收到 %q）", raw)
	}
	for _, label := range strings.Split(body, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("不是合法主机名（收到 %q）", raw)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return "", fmt.Errorf("只能包含字母、数字、-、_ 和 .（收到 %q）", raw)
			}
		}
	}
	return host, nil
}

func normalizeScopePathPrefix(raw string) (string, error) {
	prefix := strings.TrimSpace(raw)
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		return "", fmt.Errorf("必须以 / 开头（收到 %q）", raw)
	}
	if len(prefix) > 512 {
		return "", errors.New("超过了 512 个字符")
	}
	for _, r := range prefix {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("不能包含空白或控制字符（收到 %q）", raw)
		}
	}
	return prefix, nil
}

// ScopeRulesOf returns the project's stored rule set. ok=false means the
// manifest has none, which the pipeline reads as "record only".
func (p *Project) ScopeRulesOf() (ScopeRules, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ScopeRuleSet == nil {
		return ScopeRules{}, false
	}
	return *p.ScopeRuleSet, true
}

// The fence modes: the derived, operator-facing statement of what the traffic
// fence does for a project right now. They are computed from ScopeRuleSet —
// never stored — so there is exactly one source of truth and no mode field
// that could drift from the rules it claims to summarize.
const (
	// FenceUnfenced: no rule set at all. The pipeline only records; nothing is
	// held or refused. The guaranteed builtin command audit still runs.
	FenceUnfenced = "unfenced"
	// FenceShadow: every request is judged and journaled, and forwarded
	// regardless of the verdict — the trial mode.
	FenceShadow = "shadow"
	// FenceEnforced: verdicts have teeth — review holds, block refuses.
	FenceEnforced = "enforced"
	// FenceDraft: a rule set is stored but disabled; it applies nothing until
	// the operator flips the switch.
	FenceDraft = "draft"
)

// FenceModeOf classifies a rule-set pointer into a fence mode. Nil means the
// operator never configured a fence.
func FenceModeOf(rules *ScopeRules) string {
	switch {
	case rules == nil:
		return FenceUnfenced
	case !rules.Enabled:
		return FenceDraft
	case rules.Shadow:
		return FenceShadow
	default:
		return FenceEnforced
	}
}

// FenceMode is the project's current fence mode (see FenceModeOf).
func (p *Project) FenceMode() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return FenceModeOf(p.ScopeRuleSet)
}

// SetScopeRules validates and persists the operator's rule set; nil clears it
// so the project goes back to record-only with no stored draft. The value is
// normalized before the write so memory, disk and any later dispatch all see
// the same shape.
func (m *Manager) SetScopeRules(id string, rules *ScopeRules) (ScopeRules, error) {
	var normalized ScopeRules
	if rules != nil {
		var err error
		normalized, err = NormalizeScopeRules(*rules)
		if err != nil {
			return ScopeRules{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[id]
	if !ok {
		return ScopeRules{}, fmt.Errorf("没有这个项目：%s", id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	before := p.Manifest
	if rules == nil {
		p.ScopeRuleSet = nil
	} else {
		// Deep-copy the rules: the receipt the caller keeps must not alias the
		// slice the manifest now owns (same contract as the PeerPolicy receipt).
		stored := normalized
		stored.Rules = append([]ScopeRule(nil), normalized.Rules...)
		p.ScopeRuleSet = &stored
	}
	if err := p.writeOrRestore(func() { p.ScopeRuleSet = before.ScopeRuleSet }); err != nil {
		return ScopeRules{}, err
	}
	if rules == nil {
		return ScopeRules{}, nil
	}
	return normalized, nil
}
