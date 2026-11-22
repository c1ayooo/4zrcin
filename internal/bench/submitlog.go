package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Submission is one line of the submitter log: the only record that says what we
// sent and what the platform answered. The ledger says which executor found a
// flag and why it believed it; this file says what was actually submitted and
// how it was scored. Neither can be reconstructed from the other, which is why
// both leave the run in the collection zip.
//
// It carries a digest of the flag, not the flag: the raw value belongs to the
// ledger fact that found it, and this log is meant to be copied off the machine.
type Submission struct {
	UniqueCode string    `json:"unique_code"`
	FlagSHA256 string    `json:"flag_sha256"`
	FlagLength int       `json:"flag_length"`
	Correct    bool      `json:"correct"`
	Awarded    int       `json:"awarded"`
	Cumulative int       `json:"cumulative_score"`
	At         time.Time `json:"at"`
	Note       string    `json:"note,omitempty"`
}

// DigestFlag is what the log stores instead of the flag itself.
func DigestFlag(flag string) string {
	sum := sha256.Sum256([]byte(flag))
	return hex.EncodeToString(sum[:])
}

// SubmitLog appends submissions to a JSONL file, one line per attempt, and syncs
// each line before returning.
//
// The sync is deliberate: an eight-hour run can be killed at any moment, and the
// line that was still in the buffer is exactly the one that would answer "did we
// already get those points, or did we lose them to our own crash".
type SubmitLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// OpenSubmitLog opens (creating if needed) the append-only log at path.
func OpenSubmitLog(path string) (*SubmitLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &SubmitLog{path: path, f: f}, nil
}

// Append writes one line and syncs it.
func (l *SubmitLog) Append(s Submission) error {
	if s.At.IsZero() {
		s.At = time.Now()
	}
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(line); err != nil {
		return err
	}
	return l.f.Sync()
}

// Path is where the lines go, so the runbook and the collection script can name
// the file without guessing.
func (l *SubmitLog) Path() string { return l.path }

func (l *SubmitLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
