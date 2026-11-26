//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess gives the child its own process group; the launcher exiting
// does not signal it.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}
