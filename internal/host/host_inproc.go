//go:build inprochost

package host

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/host/inproc"
)

// inprocEnabled reports that this build embeds the Hub; Start accepts
// Config.Inproc only when this is true.
func inprocEnabled() bool { return true }

// mode names the launch form for host_event lines and the status panel.
func (h *Host) mode() string { return "inproc" }

// resolveStandaloneNodeBin finds cyber-scan without a Hub binary to anchor on
// (the embedded Hub has no directory to look beside).
func resolveStandaloneNodeBin() string {
	name := "cyber-scan"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path, _ := exec.LookPath(name)
	return path
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
	nodeCmd := exec.Command(h.cfg.NodeBin, nodeArgs...)
	nodeCmd.Dir = h.cfg.Dir
	nodeCmd.Env = withExcludeTools(h.childEnv(h.cfg.Env), h.cfg.ExcludeTools)
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
