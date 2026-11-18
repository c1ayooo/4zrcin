package host

import (
	"testing"
	"time"
)

// The host starts its Hub and scan as one supervised unit. setProcAttr prepares
// the tree and killGroup tears it down; the platform files supply those two
// functions (process group on POSIX, taskkill /T on Windows). This test runs the
// same semantic on both platforms: a real child process is started and
// killGroup must terminate the tree promptly. No process that this test did not
// start is ever signalled.
func TestKillGroupTerminatesTheProcessTree(t *testing.T) {
	cmd := sleeperCommand()
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	killGroup(cmd)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("process tree survived killGroup (pid %d)", cmd.Process.Pid)
	}
}

// killGroup on a command that never started must be a no-op instead of a panic
// or a signal to PID 0 (which on POSIX would hit the whole process group).
func TestKillGroupWithoutProcessIsSafe(t *testing.T) {
	cmd := sleeperCommand()
	setProcAttr(cmd)
	killGroup(cmd) // no Start: cmd.Process is nil
}
