package model

import "time"

const (
	InstructionActive  = "active"
	InstructionRevoked = "revoked"
)

// Instruction is operator guidance, never an observation or proof. An empty
// IntentID applies to the project; otherwise it applies to that intent's subtree.
type Instruction struct {
	ID              string    `json:"id"`
	IntentID        string    `json:"intent_id,omitempty"`
	Text            string    `json:"text"`
	Version         uint64    `json:"version"`
	State           string    `json:"state"`
	CreatedBy       string    `json:"created_by"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	// Authorization marks this instruction as a boundary change (the operator
	// ticked "as an authorization change" when dispatching it). It is the only
	// distinction the prompts need: brief text plus Authorization instructions
	// are the sources a boundary may come from. An instruction without it is
	// still a durable instruction — it is injected and must be followed — but it
	// is not a permission, so it never appears under the 授权来源 heading.
	Authorization  bool      `json:"authorization,omitempty"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
