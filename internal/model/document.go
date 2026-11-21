package model

import "time"

// Document is one of the three project documents the prompts are built from:
// the starting point, the goal, and the constraints. The text is the whole
// content — there are no per-line entries and no marks — and each save is a new
// revision of the same row, so the ledger's version history is the audit trail.
//
// Goal is a view over the goal object rather than a second copy: GoalID names
// the goal behind it, and saving the document edits that goal's statement.
type Document struct {
	Kind      string    `json:"kind"` // scope|goal|constraints
	Revision  uint64    `json:"revision"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	GoalID    string    `json:"goal_id,omitempty"`
}
