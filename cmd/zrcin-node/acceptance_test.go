//go:build remtunnel

package main

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zrcin/internal/tunnel"
)

// TestEndToEnd drives the whole 1a path with the real launcher binary and a
// real rem tunnel: install over the public ingress, come online through a
// tunnel, report heartbeats, then destroy from the platform and watch the
// detached uninstaller remove the node's files (docs/15 §12, the 1a items that
// can be automated).
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and drives the launcher")
	}
	tmp := t.TempDir()
	launcher := filepath.Join(tmp, "zrcin-node")
	buildLauncher(t, launcher)

	// The platform's public face: a bare listener so the join response can
	// carry its URL before anything serves it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	defer ln.Close() //nolint:errcheck

	hubAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	consoleAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	mirrorDir := filepath.Join(tmp, "mirror", "data")

	store, err := enroll.Open(filepath.Join(tmp, "platform", "remote-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	svc := enroll.New(store, enroll.Options{
		PublicURL:   base,
		DataDir:     mirrorDir,
		HubAddr:     hubAddr,
		ConsoleAddr: consoleAddr,
		HubKey:      "hub-key",
		IssueKey:    key,
	})
	tg, err := tunnel.Start(svc, tunnel.Config{
		PublicURL:   base,
		Key:         enroll.RemTunnelKey(key),
		HubAddr:     hubAddr,
		ConsoleAddr: consoleAddr,
	})
	if err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	t.Cleanup(func() { _ = tg.Close("") })
	ing := enroll.NewIngress(svc)
	ing.Register("/node/ticket", tg.TicketHandler())
	ing.RegisterPrefix("/tunnel/", tg.TunnelHandler())

	// Node artifacts: the real launcher plus a scan stub that records its pid
	// so the test can clean up after the uninstaller deletes its file.
	scanPid := filepath.Join(tmp, "scan.pid")
	scanStub := filepath.Join(tmp, "scan-stub")
	if err := os.WriteFile(scanStub, []byte("#!/bin/sh\necho $$ > "+scanPid+"\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cliStub := filepath.Join(tmp, "zrcin-cli")
	if err := os.WriteFile(cliStub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dist := enroll.NewDist(enroll.DistConfig{
		DataDir: filepath.Join(tmp, "dist"),
		Paths: map[string]string{
			enroll.ArtLauncher: launcher,
			enroll.ArtScanFull: scanStub,
			enroll.ArtScanCap:  scanStub,
			enroll.ArtCLI:      cliStub,
		},
	})
	dist.RegisterRoutes(ing, svc)

	srv := &http.Server{Handler: ing}
	go func() { _ = srv.Serve(ln) }() //nolint:errcheck
	defer srv.Close()                 //nolint:errcheck

	created, err := svc.Create(enroll.CreateOptions{Name: "edge-e2e", Recipe: enroll.RecipeFull})
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}

	// Install exactly as the bootstrap script would: the launcher's own
	// install subcommand, pointed at the join URL.
	root := filepath.Join(tmp, "root")
	installCmd := exec.Command(launcher, "install", "--join", base+"/join/"+created.Token, "--root", root)
	installCmd.Env = append(os.Environ(), "ZRCIN_NODE_NO_SERVICE=1")
	if out, err := installCmd.CombinedOutput(); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	cfg := mustLoad(t, filepath.Join(root, "etc/node.json"))
	if cfg.NodeID == "" || cfg.Credential == "" {
		t.Fatalf("install wrote an empty config: %+v", cfg)
	}

	runCmd := exec.Command(launcher, "run", "--root", root)
	var runLog strings.Builder
	runCmd.Stdout, runCmd.Stderr = &runLog, &runLog
	if err := runCmd.Start(); err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() {
		_ = runCmd.Process.Kill()
		_ = runCmd.Wait()
		if raw, err := os.ReadFile(scanPid); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil {
				_ = exec.Command("kill", strconv.Itoa(pid)).Run()
			}
		}
		if t.Failed() {
			t.Logf("launcher output:\n%s", runLog.String())
		}
	})

	// The node comes online through the tunnel and starts reporting.
	waitFor(t, 30*time.Second, "node online with a live tunnel", func() bool {
		view, ok := svc.NodeView(cfg.NodeID)
		if !ok || !view.Online {
			return false
		}
		_, up := tg.Session(cfg.NodeID)
		return up
	})
	if got := svc.ListEnrollments()[0].State; got != enroll.EnrollJoined {
		t.Fatalf("enrollment state after connect = %q, want joined", got)
	}
	if _, err := os.Stat(scanPid); err != nil {
		t.Fatalf("scan child never started: %v", err)
	}

	// Destroy from the platform: the launcher gets the directive, reports the
	// receipt and runs the detached uninstaller.
	if _, err := svc.Destroy(cfg.NodeID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	_ = tg.Close(cfg.NodeID)
	waitFor(t, 30*time.Second, "install root removed", func() bool {
		_, err := os.Stat(root)
		return os.IsNotExist(err)
	})
	waitFor(t, 15*time.Second, "node reported destroyed", func() bool {
		view, ok := svc.NodeView(cfg.NodeID)
		return ok && view.State == enroll.NodeDestroyed
	})
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Fatalf("the mirrored data dir (marked as this node's) survived the uninstall: %v", err)
	}
}

func buildLauncher(t *testing.T, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-tags", "remtunnel", "-o", out, ".")
	cmd.Env = os.Environ()
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, raw)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
