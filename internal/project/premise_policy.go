package project

import (
	"fmt"
	"strings"
)

// Premise admission has exactly one mode: verified_only (证明优先). Every
// necessary premise must be a reviewable kind, verified at its current claim
// version, with a receipt bound to the current claim and intact original
// evidence. Legacy standard/strict manifests are migrated to this value when the
// project is loaded; they are not served as a read-only compatibility mode.
const PremisePolicyVerifiedOnly = "verified_only"

// PremisePolicyView is the effective policy. There is no second mode.
func (p *Project) PremisePolicyView() string {
	return PremisePolicyVerifiedOnly
}

// SetPremisePolicy normalizes and persists the only supported mode. It is not an
// operator switch: it is called when a project is created and when a legacy
// manifest is normalized on load, so the stored value never contradicts the one
// mode that exists. No endpoint calls it.
func (p *Project) SetPremisePolicy(mode string) (string, error) {
	normalized, err := NormalizePremisePolicy(mode)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.Manifest
	p.PremisePolicy = normalized
	if err := p.writeOrRestore(func() { p.PremisePolicy = previous.PremisePolicy }); err != nil {
		return "", err
	}
	return normalized, nil
}

// Manager.SetPremisePolicy is gone on purpose: with one mode and a manifest that
// is normalized on load, an instance-level project setter could only rewrite the
// current value. Keeping it would leave a path that looks like an operator
// decision and changes nothing.

// NormalizePremisePolicy validates an operator-supplied mode before any setter
// writes it. It is exported for callers that edit several project fields in one
// request and must reject the whole request before persisting an earlier field.
func NormalizePremisePolicy(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == PremisePolicyVerifiedOnly {
		return PremisePolicyVerifiedOnly, nil
	}
	switch mode {
	case "standard", "strict":
		return "", fmt.Errorf("premise_policy 的 %s 模式已移除：唯一模式是 verified_only；旧项目会在加载时迁移", mode)
	default:
		return "", fmt.Errorf("premise_policy 只能是 verified_only（收到 %q）", mode)
	}
}
