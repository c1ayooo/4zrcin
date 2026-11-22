// Package bench is the TSecBench platform client for one measurement run: it
// lists challenges, keeps at most three containers alive, submits the flags the
// executors found, and records every answer the platform gave.
//
// Nothing in the control plane imports this package. The only entry point is the
// `tw bench` subcommand, and that subcommand refuses to run unless it was given
// both a token and a base URL. This is a rule, not a convenience: an operator's
// pause must keep waiting for a human and a met goal must keep parking the
// project, no matter that a benchmark integration exists. A new capability that
// changes how a normal engagement behaves is a worse bug than the one it fixes.
// The guard test (guard_test.go) turns that rule into a build-time fact: the
// control plane's dependency graph may not contain this package.
//
// Field names, paths and error codes follow the platform's own API document
// (CHALLENGES_API.md, five endpoints) and its SDK's exception table (SDK_API.md).
// Where the two disagree the API document wins, with one exception noted on
// IsSlotFull.
package bench

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// The signature the runner uses when it writes on the control plane's behalf.
// The literals are duplicated in internal/api, which may not import this package;
// a test there asserts the two definitions cannot drift apart.
const (
	ActorHeader = "X-Tw-Actor"
	ActorBench  = "bench"
)

// Platform machine codes, verbatim from the API document §4.2.
const (
	CodeTaskNotFound        = "task_not_found"
	CodeChallengeNotFound   = "challenge_not_found"
	CodeInvalidState        = "invalid_state"
	CodeDuplicate           = "duplicate"
	CodeResourceUnavailable = "resource_unavailable"
	CodeInternalError       = "internal_error"
)

// Failure is a platform answer that is not plain success.
type Failure struct {
	Status  int
	Code    string
	Message string
}

func (f *Failure) Error() string {
	if f.Code == "" {
		return fmt.Sprintf("platform %d: %s", f.Status, f.Message)
	}
	return fmt.Sprintf("platform %d %s: %s", f.Status, f.Code, f.Message)
}

// SlotFull is the container-ceiling refusal. The API document gives it no code
// of its own — `start` answers 409 invalid_state both when the task is over and
// when three containers are already active — so the message is the only
// discriminator, exactly as the SDK does it (`"max active" in e.message`).
// Getting these two the wrong way round is expensive in both directions: a run
// that treats "task over" as "wait for a slot" spins for the rest of the window,
// and a run that treats "wait for a slot" as "task over" quits at three.
func IsSlotFull(err error) bool {
	var f *Failure
	if !errors.As(err, &f) {
		return false
	}
	return f.Code == CodeInvalidState && strings.Contains(strings.ToLower(f.Message), "max active")
}

// Kind is what the caller may do about a failure.
type Kind int

const (
	// KindTerminal: do not retry. The task is over (invalid_state without the
	// slot marker), the token or challenge is wrong, or the platform refused
	// something only a human can change. This is the zero value on purpose: an
	// unclassified failure must stop the run, because an eight-hour unattended
	// run that spins on an unknown error looks alive while burning the window.
	KindTerminal Kind = iota
	// KindRetryable: a passing shortage of platform resources (503
	// resource_unavailable). Wait and retry; nothing is wrong with the request.
	KindRetryable
	// KindDuplicate: the idempotency guard fired. The platform already recorded
	// this exact flag, so for us it is success, not an error.
	KindDuplicate
	// KindSlotFull: three containers are already active. Release one (or learn
	// which ones the platform counts), then try this challenge again — it is a
	// queueing situation, not an error.
	KindSlotFull
)

func (k Kind) String() string {
	switch k {
	case KindRetryable:
		return "retryable"
	case KindDuplicate:
		return "duplicate"
	case KindSlotFull:
		return "slot_full"
	default:
		return "terminal"
	}
}

// Classify turns one platform response into nil (success) or a *Failure.
func Classify(status int, code, message string) error {
	if status >= 200 && status < 300 && code == "" {
		return nil
	}
	return &Failure{Status: status, Code: code, Message: message}
}

// KindOf says how to react to err. Anything not produced by Classify is terminal
// too: a decoding bug or a dead connection must surface, not be retried forever.
func KindOf(err error) Kind {
	if err == nil {
		return KindTerminal
	}
	if IsSlotFull(err) {
		return KindSlotFull
	}
	var f *Failure
	if errors.As(err, &f) {
		switch {
		case f.Code == CodeDuplicate:
			return KindDuplicate
		case f.Code == CodeResourceUnavailable || f.Status == http.StatusServiceUnavailable:
			return KindRetryable
		}
	}
	return KindTerminal
}
