//go:build inprochost

package host

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/host/inproc"
)

// inprocEnabled reports that this build embeds the Hub; Start accepts
// Config.Inproc only when this is true.
func inprocEnabled() bool { return true }

// mode names the launch form for host_event lines and the status panel.
func (h *Host) mode() string { return "inproc" }

// selfNode is the embedded build's default node binary: this process's own
// executable (docs/19 §2 本地模式). os.Executable resolves the real file, so
// renames, moves and symlinks do not matter.
func selfNode() (string, error) { return os.Executable() }

// resolveSelfNode fills the node binary and argv prefix for this build. An
// explicit NodeBin (--scan) wins untouched and prefix-free — that is the
// escape hatch for read-only mounts and environments that forbid self-exec;
// the default is the control plane's own executable speaking the node verb.
func resolveSelfNode(cfg Config) (string, []string, error) {
	if strings.TrimSpace(cfg.NodeBin) != "" {
		return cfg.NodeBin, nil, nil
	}
	bin, err := selfNode()
	if err != nil {
		return "", nil, fmt.Errorf("解析本可执行文件：%w", err)
	}
	return bin, []string{"node"}, nil
}

// inprocLaunch is the in-process counterpart of execLaunch (docs/04 M1): the
// Hub runs inside this process on a listener it owns, the scan node stays a
// subprocess with the same environment and arguments the exec form gives it.
// The supervised-unit semantics are preserved — whichever side exits first
// tears the other down, and the returned process carries the aggregate liveness
// the supervisor already drives (done/wait/kill).
func (h *Host) inprocLaunch() (*process, error) {
	ln, err := net.Listen("tcp", strings.TrimPrefix(h.addr, "http://"))
	if err != nil {
		return nil, err
	}
	// The Hub owns the listener once Serve starts and closes it on return
	// (patch 22); a restart re-opens it on the same address.
	serveCtx, cancelServe := context.WithCancel(h.ctx)
	serveDone := make(chan struct{})
	var serveErr error
	args := append(append([]string(nil), h.execArgs()...), h.cfg.Args...)
	// The Hub's telemetry stream lands in this process's log; ANSI color is a
	// child-terminal feature, not a log feature.
	args = append(args, "--no-color")
	go func() {
		defer close(serveDone)
		serveErr = inproc.Run(serveCtx, ln, args, h.cfg.Logf)
		if serveErr != nil && serveCtx.Err() == nil {
			h.cfg.Logf("host_event=inproc_serve_error err=%v", serveErr)
		}
	}()

	// Hub-only form (--no-local-node): the serve loop is the whole supervised
	// unit; there is no node subprocess to launch or guard.
	if h.cfg.NoLocalNode {
		return &process{
			pid:     os.Getpid(),
			nodePID: 0,
			done:    serveDone,
			wait:    func() error { <-serveDone; return serveErr },
			kill:    cancelServe,
		}, nil
	}

	// The scan node: same env (CYBER_EXCLUDE_TOOLS included — its consumers
	// are scan-side), same --config document, same process-group guards as
	// the exec form's node half.
	extra := append([]string(nil), h.cfg.Args...)
	nodeArgs := h.nodeArgs(extra)
	if nodeArgs[len(nodeArgs)-1] == h.db+".node.yaml" {
		if err := os.WriteFile(h.db+".node.yaml", []byte("{}\n"), 0o600); err != nil {
			cancelServe()
			return nil, err
		}
	}
	nodeCmd := exec.Command(h.cfg.NodeBin, nodeArgv(h.cfg.NodePrefix, nodeArgs)...)
	nodeCmd.Dir = h.cfg.Dir
	nodeCmd.Env = withRulesDir(withExcludeTools(h.childEnv(h.cfg.Env), h.cfg.ExcludeTools), h.cfg.RulesDir)
	setProcAttr(nodeCmd)
	nodeCmd.WaitDelay = 10 * time.Second
	nodeCmd.Stdout = &lineWriter{h: h, name: "scan/out"}
	nodeCmd.Stderr = &lineWriter{h: h, name: "scan/err"}
	if err := nodeCmd.Start(); err != nil {
		cancelServe()
		return nil, err
	}
	if err := guardChild(nodeCmd); err != nil {
		h.cfg.Logf("host_event=job_assign_failed name=scan pid=%d err=%v", nodeCmd.Process.Pid, err)
	}

	done := make(chan struct{})
	var result error
	go func() {
		exits := make(chan error, 2)
		go func() {
			<-serveDone
			exits <- serveErr
		}()
		go func() { exits <- nodeCmd.Wait() }()
		result = <-exits
		// Whichever side ended first, bring the other down and wait for it.
		cancelServe()
		killGroup(nodeCmd)
		<-exits
		close(done)
	}()
	return &process{
		pid:     os.Getpid(),
		nodePID: nodeCmd.Process.Pid,
		done:    done,
		wait:    func() error { <-done; return result },
		kill: func() {
			cancelServe()
			killGroup(nodeCmd)
		},
	}, nil
}
