//go:build !windows && !linux

package host

import "os/exec"

// setDeathSignal is a no-op off Linux: macOS and the BSDs expose no
// parent-death signal, so the only protection there is the normal stop path
// (killGroup on the child's process group) plus the orphan sweep at the next
// start. Stated here so the gap is visible instead of being mistaken for
// parity with Windows, which has the job object.
func setDeathSignal(*exec.Cmd) {}
