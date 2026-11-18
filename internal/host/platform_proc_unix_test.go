//go:build !windows

package host

import (
	"os/exec"
	"syscall"
	"testing"
)

// sleeperCommand returns a long-running child. The host's PTY/tool children are
// in the same process group, so the group kill is what the test exercises.
func sleeperCommand() *exec.Cmd {
	return exec.Command("sleep", "60")
}

// POSIX side of the process-tree semantic: setProcAttr must put the host into
// its own process group so one negative-PID signal reaches its children too.
// The Windows counterpart (platform_proc_windows_test.go) asserts the HideWindow
// half of the same semantic; both are necessary for killGroup to be safe.
func TestSetProcAttrPreparesPOSIXGroup(t *testing.T) {
	cmd := sleeperCommand()
	setProcAttr(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %+v, want Setpgid", cmd.SysProcAttr)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != cmd.Process.Pid {
		t.Fatalf("pgid = %d, want the child's own pid %d", pgid, cmd.Process.Pid)
	}
}
