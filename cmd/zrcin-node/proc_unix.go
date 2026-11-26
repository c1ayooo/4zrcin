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
