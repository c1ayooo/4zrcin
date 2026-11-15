package model

import "time"

// NoteRef identifies preserved note bytes and their authenticated writer. A run
// is the durable round identity; Round is only its human-readable sequence.
type NoteRef struct {
	Version uint64 `json:"version"`
	SHA256  string `json:"sha256"`
	Writer  string `json:"writer"`
	RunID   string `json:"run_id,omitempty"`
	Round   int    `json:"round,omitempty"`
}

// Notebook is private judgement, never a fact or a source of authorization.
// Previous versions are preserved by the ledger's ordinary version journal.
type Notebook struct {
	ID string `json:"id"`
	NoteRef
	State             string            `json:"state"`
	WrittenAt         time.Time         `json:"written_at"`
	FactVersions      map[string]uint64 `json:"fact_versions,omitempty"`
	FactClaimVersions map[string]uint64 `json:"fact_claim_versions,omitempty"`
}
