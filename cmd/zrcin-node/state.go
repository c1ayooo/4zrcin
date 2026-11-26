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
	Updated string `json:"updated,omitempty"`
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
