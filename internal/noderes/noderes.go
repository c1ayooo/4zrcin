// Package noderes samples one host's resource facts for the node cards
// (docs/14 §7.3): CPU utilisation over a short window, memory, the data
// directory's disk, and the scan process's resident set. Everything is read
// from the OS directly — no new dependency — and what a platform cannot know
// on its kernel stays a zero-valued field the console renders as 不支持.
//
// The same sampler serves both ends of the feature: the launcher embeds it to
// report with every heartbeat, and the control plane embeds it to show the
// local node's own host.
package noderes

import (
	"time"
)

// Snapshot is one sample. Percentages are 0–100. A zero MemTotal/DiskTotal
// means the platform could not read that counter on this kernel.
//
// atomic.Value call sites wrap it in their own named type; Snapshot itself
// stays a plain struct so JSON keeps its shape.
type Snapshot struct {
	CollectedAt time.Time `json:"collected_at"`
	// CPUPercent is the busy fraction over the sampler's window (about one
	// second), averaged over every core. 0 on the first call of a fresh
	// sampler and on unsupported kernels.
	CPUPercent float64 `json:"cpu_percent"`
	// MemUsed/MemTotal are the host's usable memory (MemTotal minus
	// MemAvailable on Linux), bytes. 0 total = unsupported.
	MemUsed  uint64 `json:"mem_used"`
	MemTotal uint64 `json:"mem_total"`
	// DiskFree/DiskTotal describe the filesystem holding the data directory —
	// the volume that fills with cases, evidence and workspaces.
	DiskFree  uint64 `json:"disk_free"`
	DiskTotal uint64 `json:"disk_total"`
	// ScanRSS is the scan process's current resident set, bytes. 0 = the
	// process is gone or unreadable.
	ScanRSS uint64 `json:"scan_rss,omitempty"`
}
