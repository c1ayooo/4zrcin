//go:build !inprochost

package host

import (
	"errors"
	"os/exec"
	"runtime"
)

// inprocEnabled is false: this build does not embed the Hub, so Start refuses
// Config.Inproc up front and the exec form is the only launch path.
func inprocEnabled() bool { return false }

// mode names the launch form for host_event lines and the status panel.
func (h *Host) mode() string { return "exec" }

// resolveStandaloneNodeBin finds cyber-scan on PATH without a Hub binary to
// anchor on. Unreachable in this form (Start refuses Inproc), kept for the
// shared compile surface.
func resolveStandaloneNodeBin() string {
	name := "cyber-scan"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path, _ := exec.LookPath(name)
	return path
}

// inprocLaunch is unreachable in this form — Start refuses Config.Inproc
// before the first launch attempt. The stub keeps the tagged-out call site
// compiling and fails loudly if that guard ever regresses.
func (h *Host) inprocLaunch() (*process, error) {
	return nil, errors.New("此构建未内嵌 Hub：inproc 启动不应到达这里（Start 应先拒绝）")
}
