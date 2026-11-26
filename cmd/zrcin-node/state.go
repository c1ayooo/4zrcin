package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// runState is the small status file `zrcin-node status` reads: what the
// service knows about its own runtime, written by the tunnel and scan loops.
// Advisory only — the platform's view comes from heartbeats — but it is what
// an operator can read on the host without the console.
type runState struct {
	Tunnel  string `json:"tunnel"` // "up" | "down" | ""
	ScanPID int    `json:"scan_pid,omitempty"`
	// ScanRestarts counts restarts by this launcher process; the health rule
	// reads it as "the scan keeps dying" (docs/14 §7.3).
	ScanRestarts int    `json:"scan_restarts,omitempty"`
	Started      string `json:"started,omitempty"` // launcher process start, RFC3339
	Updated      string `json:"updated,omitempty"`
}

// stateWriter serialises the writers and owns the path.
type stateWriter struct {
	mu    sync.Mutex
	path  string
	state runState
}

func statePath(cfg *Config) string { return filepath.Join(cfg.Root, "var", "run-state.json") }

func newStateWriter(cfg *Config) *stateWriter {
	return &stateWriter{path: statePath(cfg)}
}

func (w *stateWriter) setTunnel(v string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state.Tunnel = v
	w.persistLocked()
}

// snapshot returns a copy of the current runtime state. The heartbeat watcher
// compares copies to detect tunnel/scan transitions without holding the lock.
func (w *stateWriter) snapshot() (runState, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Tunnel == "" && w.state.ScanPID == 0 {
		return w.state, false // nothing has reported yet
	}
	return w.state, true
}

// markStarted stamps the launcher process start once.
func (w *stateWriter) markStarted() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Started == "" {
		w.state.Started = time.Now().UTC().Format(time.RFC3339)
		w.persistLocked()
	}
}

// scanPIDOf reads the current scan pid for the sampler.
func scanPIDOf(w *stateWriter) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state.ScanPID
}

// bumpScanRestarts records one more supervised-scan restart.
func (w *stateWriter) bumpScanRestarts() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state.ScanRestarts++
	w.persistLocked()
}

// restartCount reports the scan restart count for the heartbeat payload.
func (w *stateWriter) restartCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state.ScanRestarts
}

// startedAt is when this launcher process started (the card's 运行时长).
func (w *stateWriter) startedAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Started == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339, w.state.Started)
	if err != nil {
		return time.Now()
	}
	return t
}

func (w *stateWriter) setScan(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state.ScanPID = pid
	w.persistLocked()
}

func (w *stateWriter) persistLocked() {
	w.state.Updated = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(w.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(w.path), 0o755)
	_ = os.WriteFile(w.path, raw, 0o644)
}

func loadRunState(path string) runState {
	raw, err := os.ReadFile(path)
	if err != nil {
		return runState{}
	}
	var st runState
	_ = json.Unmarshal(raw, &st)
	return st
}
