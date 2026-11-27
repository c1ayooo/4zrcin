//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own session, so the launcher exiting (or
// its service being stopped) does not take the uninstaller down with it.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminateProcess asks a child to stop the way a deliberate restart means
// it: TERM, so it can close what it holds, rather than the kill ctx uses.
func terminateProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
