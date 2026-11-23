package store

import (
	"fmt"
	"strings"

	"github.com/kksty/tianwu/internal/evidence"
	"github.com/kksty/tianwu/internal/model"
)

// normalizeEvidence keeps a row's preservation metadata structurally coherent
// before it can be written. It is not a read-time check (evidence.Read does
// that): it makes the impossible states impossible to record in the first place.
//
// A URI inside the content-addressed store is only meaningful together with the
// digest encoded in its name; accepting such a row without a digest would create
// an "immutable" row no reader can verify. Legacy rows (ordinary working-path
// URIs, no digest) pass untouched — their limitation is explicit, not repaired.
func normalizeEvidence(e model.Evidence) (model.Evidence, error) {
	e.Digest = strings.ToLower(strings.TrimSpace(e.Digest))
	if e.Size < 0 {
		return e, fmt.Errorf("证据 %s 的大小不能为负：%d", evidenceName(e), e.Size)
	}
	if e.Digest != "" && !isSHA256Hex(e.Digest) {
		return e, fmt.Errorf("证据 %s 的摘要不是 SHA-256 hex：%q", evidenceName(e), e.Digest)
	}
	if digest, preserved := evidence.ObjectDigest(e.URI); preserved {
		if e.Digest == "" {
			return e, fmt.Errorf("证据 %s 指向保全副本 %s，却没有记录摘要", evidenceName(e), e.URI)
		}
		if digest != e.Digest {
			return e, fmt.Errorf("证据 %s 的摘要 %s 与保全副本名 %s 不一致", evidenceName(e), e.Digest, e.URI)
		}
	}
	return e, nil
}

// EvidenceVerifiable reports whether a registered original still carries the
// digest its bytes can be checked against. Readiness (internal/api) and the
// dispatch gate in PrepareRunInput both read this one predicate: without it the
// two answered differently — the page said "原件缺失或没有保全摘要" while dispatch
// only asked whether the row id existed, so a legacy row with an empty digest was
// refused on screen and admitted in practice.
func EvidenceVerifiable(e model.Evidence) bool {
	return strings.TrimSpace(e.Digest) != ""
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func evidenceName(e model.Evidence) string {
	switch {
	case e.ID != "":
		return e.ID
	case e.URI != "":
		return e.URI
	default:
		return "（无 id/uri）"
	}
}
