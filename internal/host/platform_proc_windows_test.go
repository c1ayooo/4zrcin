//go:build windows

package host

import (
	"os/exec"
	"testing"
)

// sleeperCommand returns a long-running child whose own child is a second
// process, so /T (tree) semantics are exercised, not just a single PID.
func sleeperCommand() *exec.Cmd {
	return exec.Command("cmd", "/c", "ping", "-n", "60", "127.0.0.1")
}

// Windows side of the process-tree semantic: the host must be launched hidden
// (no console window) while taskkill /T handles the tree. The POSIX counterpart
// (platform_proc_unix_test.go) asserts the process-group half of the same
// semantic.
func TestSetProcAttrPreparesWindowsTree(t *testing.T) {
	cmd := sleeperCommand()
	setProcAttr(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("SysProcAttr = %+v, want HideWindow", cmd.SysProcAttr)
	}
}
