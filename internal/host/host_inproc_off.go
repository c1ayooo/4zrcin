//go:build !inprochost

package host

import (
	"errors"
)

// inprocEnabled is false: this build does not embed the Hub, so Start refuses
// Config.Inproc up front and the exec form is the only launch path.
func inprocEnabled() bool { return false }

// mode names the launch form for host_event lines and the status panel.
func (h *Host) mode() string { return "exec" }

// resolveSelfNode is unreachable in this form — Start refuses Config.Inproc
// before node resolution, and this build has no node assembly to self-execute
// anyway. The stub keeps the shared call site compiling and fails loudly if
// that guard ever regresses.
func resolveSelfNode(cfg Config) (string, []string, error) {
	return cfg.NodeBin, nil, errors.New("此构建未内嵌节点装配：self-node 不应到达这里（Start 应先拒绝 Inproc）")
}

// inprocLaunch is unreachable in this form — Start refuses Config.Inproc
// before the first launch attempt. The stub keeps the tagged-out call site
// compiling and fails loudly if that guard ever regresses.
func (h *Host) inprocLaunch() (*process, error) {
	return nil, errors.New("此构建未内嵌 Hub：inproc 启动不应到达这里（Start 应先拒绝）")
}
