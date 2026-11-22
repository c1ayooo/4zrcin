//go:build !windows

package host

import "os/exec"

// guardChild is a no-op off Windows: killGroup puts each child in its own
// process group, and a graceful stop kills the group explicitly. Unix has no
// kill-on-parent-death for a whole tree without cgroups, and the control plane
// already stops its host on every exit path (see Host.StopWithin).
func guardChild(*exec.Cmd) error { return nil }