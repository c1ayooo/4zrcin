package model

import "time"

// Asset kinds name the fence-facing identity an asset record carries. A range
// is a CIDR block; a domain row covers the bare domain and its whole subdomain
// tree (the fence's own matching semantics), so "one domain" is one row even
// when imported spellings differ.
const (
	AssetKindDomain  = "domain"
	AssetKindHost    = "host"
	AssetKindService = "service"
	AssetKindRange   = "range"
)

// Fence membership is the asset's standing relative to the project fence.
// in_fence  = the fence forwards it; pending = refused, awaiting the
// operator's decision; denied = refused and decided against.
const (
	AssetFenceIn      = "in_fence"
	AssetFencePending = "pending"
	AssetFenceDenied  = "denied"
)

// Sources record how the asset entered the table: seeded by a fence row at
// creation, confirmed by an operator import, or surfaced by observed traffic
// and facts.
const (
	AssetSourceFence    = "fence"
	AssetSourceImport   = "import"
	AssetSourceObserved = "observed"
)

// Asset is one fence-facing identity in the project's asset table. Identity is
// (Kind, Key) — a repeated flow updates the same row instead of duplicating it.
//
// Target records the operator's own classification (authorized target vs.
// infrastructure the tools merely need); the fence never derives it.
type Asset struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Key         string    `json:"key"`
	Target      bool      `json:"target"`
	Fence       string    `json:"fence"`
	Source      string    `json:"source"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IsAssetKind reports whether kind names an asset row.
func IsAssetKind(kind string) bool {
	switch kind {
	case AssetKindDomain, AssetKindHost, AssetKindService, AssetKindRange:
		return true
	}
	return false
}
