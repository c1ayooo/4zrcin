//go:build !remtunnel

package relay

import (
	"context"
	"errors"
	"time"
)

// ErrNotCompiled is the answer of a build without the rem checkout: the
// reverse connection is a feature of the tunneled build.
var ErrNotCompiled = errors.New("本次构建未编入隧道支持：用 -tags remtunnel 重新构建 zrcin")

// Config mirrors the tunneled build's shape so callers compile on both.
type Serve struct {
	Listen string
	Dest   string
}

type Config struct {
	Server   string
	Key      string
	Serves   []Serve
	Console  string
	ProbeURL string
	Logf     func(format string, args ...any)
}

// State is the console-facing state.
type State struct {
	Up     bool      `json:"up"`
	Since  time.Time `json:"since,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// Relay is a placeholder; only Prepare's error is meaningful here.
type Relay struct{}

// Prepare refuses: the binary carries no rem client.
func Prepare(Config) (*Relay, error) { return nil, ErrNotCompiled }

// Run never returns in the tunneled build; here it just waits.
func (r *Relay) Run(ctx context.Context) { <-ctx.Done() }

// Close is a no-op.
func (r *Relay) Close() {}

// Status is unknown.
func (r *Relay) Status() State { return State{Detail: ErrNotCompiled.Error()} }
