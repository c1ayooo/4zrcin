package host

import (
	"bytes"
	"strconv"
)

// The harness children of one instance — the Hub, the scan node, and every
// tool/PoC process they spawn — are the only processes on this machine that can
// grow to tens of gigabytes in one round. Their peak resident set is sampled
// once per run close (internal/api calls ChildPeakRSS) so that after a memory
// incident an operator can name the process instead of guessing. This is
// bookkeeping only: nothing is capped, killed, or scheduled by it.
//
// "Unavailable" is a first-class answer, not an empty value: a process that has
// already exited has no peak left to read, and reporting 0 bytes would read as
// "it stayed tiny" — the opposite of the incident this exists to explain.

// PeakRSS is one child process's peak resident memory.
type PeakRSS struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Bytes uint64 `json:"peak_rss_bytes"`
	// Available reports whether Bytes holds a real reading. A false value with
	// a Reason is a complete answer, not a failure to hide.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// PeakRSSWarnBytes is the reporting threshold: a run whose child process peaked
// above it gets one warning line at close. It is not a cap and does not stop
// anything.
const PeakRSSWarnBytes = 4 << 30

// ChildPeakRSS samples every process this host started and everything those
// processes spawned. A supervised host whose children are gone reports one
// unavailable entry rather than an empty slice: nil means "this instance
// supervises no process at all", which is the caller's cue to record nothing.
func (h *Host) ChildPeakRSS() []PeakRSS {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	var roots []int
	if h.cmd != nil && h.running {
		if h.cmd.pid > 0 {
			roots = append(roots, h.cmd.pid)
		}
		if h.cmd.nodePID > 0 {
			roots = append(roots, h.cmd.nodePID)
		}
	}
	h.mu.Unlock()
	if len(roots) == 0 {
		return []PeakRSS{{Reason: "宿主当前没有运行中的子进程"}}
	}
	return childPeakRSS(roots)
}

// parseVmHWM reads VmHWM (peak resident set) out of one /proc/<pid>/status
// document. The second result says whether the line was there: a missing field
// or an unexpected unit must not become a zero reading.
func parseVmHWM(status []byte) (uint64, bool) {
	for _, line := range bytes.Split(status, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("VmHWM:")) {
			continue
		}
		fields := bytes.Fields(line[len("VmHWM:"):])
		if len(fields) < 2 || !bytes.Equal(fields[1], []byte("kB")) {
			return 0, false
		}
		kb, err := strconv.ParseUint(string(fields[0]), 10, 64)
		if err != nil {
			return 0, false
		}
		// The kernel prints kB; the journal records bytes so a reader does not
		// have to remember which unit one field used.
		return kb * 1024, true
	}
	return 0, false
}

// parsePgrp returns the process group from one /proc/<pid>/stat line. The comm
// field is parenthesised and may itself contain spaces or ')', so the remaining
// fields are located from the last ')' instead of a fixed field index.
func parsePgrp(stat []byte) (int, bool) {
	close := bytes.LastIndexByte(stat, ')')
	if close < 0 {
		return 0, false
	}
	fields := bytes.Fields(stat[close+1:])
	// After comm come state, ppid, pgrp: pgrp is the third of them.
	if len(fields) < 3 {
		return 0, false
	}
	pgrp, err := strconv.Atoi(string(fields[2]))
	if err != nil || pgrp <= 0 {
		return 0, false
	}
	return pgrp, true
}
