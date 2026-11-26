package model

import "time"

const (
	InstructionActive  = "active"
	InstructionRevoked = "revoked"
)

// Instruction is operator guidance, never an observation or proof. An empty
// IntentID applies to the project; otherwise it applies to that intent's subtree.
type Instruction struct {
	ID       string `json:"id"`
	IntentID string `json:"intent_id,omitempty"`
	// AssetRef names the asset the instruction governs (spec #1 工单2). It is
	// create-time identity for asset-scoped listing, not prompt content: the
	// injection surface renders Text only, and the update surface keeps text
	// and state as its mutable fields — rebinding means revoke and recreate.
	AssetRef        string `json:"asset_ref,omitempty"`
	Text            string `json:"text"`
	Version         uint64 `json:"version"`
	State           string `json:"state"`
	CreatedBy       string `json:"created_by"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	// An instruction is operator guidance that every run must follow. It is not
	// a permission: the boundary comes from the project documents alone, so
	// there is no per-instruction mark to confuse with authorization.
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
