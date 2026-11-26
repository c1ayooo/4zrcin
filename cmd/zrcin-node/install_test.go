package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// fakePlatform is the smallest platform the install path talks to: join,
// ticket, and the two artifact downloads with their digests.
type fakePlatform struct {
	t           *testing.T
	srv         *httptest.Server
	dataDir     string
	hubAddr     string
	consoleAddr string
	launcher    string
	scan        string
	joinCalls   atomic.Int32
	ticketGone  atomic.Bool
	nextMu      sync.Mutex
	next        []map[string]any
	results     chan enroll.ShellResult // what the launcher posted to /node/result
}

const (
	testToken      = "zr1_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testCredential = "zn1_0123456789abcdef0123456789abcdef_" + "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	testNodeID     = "0123456789abcdef0123456789abcdef"
)

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{
		t:           t,
		dataDir:     filepath.Join(t.TempDir(), "mirror", "data"),
		hubAddr:     fmt.Sprintf("127.0.0.1:%d", mustFreePort(t)),
		consoleAddr: fmt.Sprintf("127.0.0.1:%d", mustFreePort(t)),
		launcher:    "launcher-bytes",
		scan:        "scan-bytes",
		results:     make(chan enroll.ShellResult, 16),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/node/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req enroll.JoinRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.InstallID == "" {
			http.Error(w, `{"error":"missing install id"}`, http.StatusConflict)
			return
		}
		fp.joinCalls.Add(1)
		_ = json.NewEncoder(w).Encode(enroll.JoinResponse{
			NodeID:         testNodeID,
			NodeCredential: testCredential,
			PublicURL:      fp.srv.URL,
			Tunnel:         enroll.TunnelParams{Key: "tunnel-key-hex", Path: "/tunnel"},
			Hub:            enroll.HubParams{Addr: fp.hubAddr, Key: "hub-key"},
			ConsoleAddr:    fp.consoleAddr,
			DataDir:        fp.dataDir,
			Dist:           []enroll.DistEntry{},
		})
	})
	mux.HandleFunc("/node/next", func(w http.ResponseWriter, r *http.Request) {
		if fp.ticketGone.Load() {
			http.Error(w, `{"error":"节点已销毁"}`, http.StatusGone)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testCredential {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		fp.nextMu.Lock()
		queue := fp.next
		fp.next = nil
		fp.nextMu.Unlock()
		if queue == nil {
			queue = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": map[string]any{"id": testNodeID}, "directives": queue})
	})
	mux.HandleFunc("/node/ticket", func(w http.ResponseWriter, r *http.Request) {
		if fp.ticketGone.Load() {
			http.Error(w, `{"error":"节点已销毁"}`, http.StatusGone)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testCredential {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ticket": "t1", "url": fp.srv.URL + "/tunnel/t1?wrapper=raw",
		})
	})
	mux.HandleFunc("/node/result", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testCredential {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var res enroll.ShellResult
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil || res.RequestID == "" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		select {
		case fp.results <- res:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	serveArtifact := func(name, content string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+testCredential {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			sum := sha256.Sum256([]byte(content))
			w.Header().Set("X-Checksum-Sha256", hex.EncodeToString(sum[:]))
			_, _ = w.Write([]byte(content))
		}
	}
	mux.HandleFunc("/node/dist/cyber-scan", serveArtifact("cyber-scan", "scan-bytes"))
	mux.HandleFunc("/node/dist/zrcin", serveArtifact("zrcin", "cli-bytes"))
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakePlatform) joinURL() string { return fp.srv.URL + "/join/" + testToken }

func TestInstallWritesTheDocumentedLayout(t *testing.T) {
	fp := newFakePlatform(t)
	t.Setenv("ZRCIN_NODE_NO_SERVICE", "1")
	root := filepath.Join(t.TempDir(), "root")

	if err := runInstall(context.Background(), fp.joinURL(), root); err != nil {
		t.Fatalf("install: %v", err)
	}
	if fp.joinCalls.Load() != 1 {
		t.Fatalf("join calls = %d, want 1", fp.joinCalls.Load())
	}

	// Binaries: content and mode.
	for name, want := range map[string]string{"cyber-scan": "scan-bytes", "zrcin": "cli-bytes"} {
		p := filepath.Join(root, "bin", name)
		raw, err := os.ReadFile(p)
		if err != nil || string(raw) != want {
			t.Fatalf("%s = %q err %v", p, raw, err)
		}
		info, _ := os.Stat(p)
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s is not executable: %o", p, info.Mode().Perm())
		}
	}
	// Config: 0600, carries the join response and the local choices.
	info, err := os.Stat(filepath.Join(root, "etc/node.json"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	cfg := mustLoad(t, filepath.Join(root, "etc/node.json"))
	if cfg.NodeID != testNodeID || cfg.Credential != testCredential || cfg.HubKey != "hub-key" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.Service != "none" {
		t.Fatalf("service = %q, want none in the test environment", cfg.Service)
	}
	// Ownership markers: the uninstaller may only delete marked paths.
	if ownerOf(root) != testNodeID {
		t.Fatal("install root lacks its ownership marker")
	}
	if ownerOf(fp.dataDir) != testNodeID {
		t.Fatal("mirrored data dir lacks its ownership marker")
	}
}

func mustLoad(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return cfg
}

func TestReinstallIsRepairAndDoesNotJoinAgain(t *testing.T) {
	fp := newFakePlatform(t)
	t.Setenv("ZRCIN_NODE_NO_SERVICE", "1")
	root := filepath.Join(t.TempDir(), "root")
	if err := runInstall(context.Background(), fp.joinURL(), root); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := runInstall(context.Background(), fp.joinURL(), root); err != nil {
		t.Fatalf("repair install: %v", err)
	}
	if fp.joinCalls.Load() != 1 {
		t.Fatalf("repair joined again (%d join calls); it must reuse the stored credential", fp.joinCalls.Load())
	}
}

func TestInstallPrechecksRefuseBeforeWriting(t *testing.T) {
	fp := newFakePlatform(t)
	t.Setenv("ZRCIN_NODE_NO_SERVICE", "1")

	// Occupy the Hub mirror port the join response names.
	_, hubPort, ok := hostPortSplit(fp.hubAddr)
	if !ok {
		t.Fatalf("fake hub addr %q", fp.hubAddr)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+hubPort)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "root")
	err = runInstall(context.Background(), fp.joinURL(), root)
	if err == nil || !strings.Contains(err.Error(), "已被占用") {
		t.Fatalf("install with the Hub port taken = %v, want an occupied-port error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "etc/node.json")); !os.IsNotExist(statErr) {
		t.Fatal("a failed precheck wrote a config")
	}
	_ = ln.Close()

	// A data path that is not ours must not be adopted.
	other := t.TempDir()
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	fp.dataDir = other
	root2 := filepath.Join(t.TempDir(), "root")
	err = runInstall(context.Background(), fp.joinURL(), root2)
	if err == nil || !strings.Contains(err.Error(), "不属于本节点") {
		t.Fatalf("install over a foreign data dir = %v, want an ownership error", err)
	}
}

func mustFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSplitJoinURL(t *testing.T) {
	base, token, err := splitJoinURL("https://ops.example.com/join/zr1_abc")
	if err != nil || base != "https://ops.example.com" || token != "zr1_abc" {
		t.Fatalf("splitJoinURL = %q %q %v", base, token, err)
	}
	base, token, err = splitJoinURL("http://127.0.0.1:8080/prefix/join/zr1_abc/")
	if err != nil || base != "http://127.0.0.1:8080/prefix" || token != "zr1_abc" {
		t.Fatalf("prefixed splitJoinURL = %q %q %v", base, token, err)
	}
	if _, _, err := splitJoinURL("https://ops.example.com/missing"); err == nil {
		t.Fatal("a URL without /join/ was accepted")
	}
}

func TestScanArgsMirrorTheLocalHost(t *testing.T) {
	cfg := &Config{NodeID: testNodeID, Name: "edge-01", HubAddr: "127.0.0.1:18766", HubKey: "hub-key"}
	args := strings.Join(scanArgs(cfg), " ")
	for _, want := range []string{
		"agent --transport web",
		"--server-url http://hub-key@127.0.0.1:18766",
		"--node-id " + testNodeID,
		"--node-name edge-01",
		"--observe http,artifacts",
		"--timeout 0",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("scan args lack %q: %s", want, args)
		}
	}
}

func TestFetchTicket(t *testing.T) {
	fp := newFakePlatform(t)
	cfg := &Config{PublicURL: fp.srv.URL, Credential: testCredential}
	url, err := fetchTicket(context.Background(), cfg)
	if err != nil || !strings.Contains(url, "/tunnel/t1") {
		t.Fatalf("fetchTicket = %q %v", url, err)
	}
	fp.ticketGone.Store(true)
	if _, err := fetchTicket(context.Background(), cfg); err != errRevoked {
		t.Fatalf("fetchTicket on a destroyed node = %v, want errRevoked", err)
	}
}

// The supervisor restarts a crashing scan and exits with ctx.
func TestSuperviseScanRestartsAndStops(t *testing.T) {
	dir := t.TempDir()
	countFile := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "scan")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho run >> "+countFile+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{ScanBin: script, DataDir: dir, NodeID: testNodeID, Name: "n", HubAddr: "127.0.0.1:1", HubKey: "k"}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { superviseScan(ctx, cfg, &stateWriter{path: filepath.Join(dir, "run-state.json")}); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("superviseScan did not stop with ctx")
	}
	raw, err := os.ReadFile(countFile)
	if err != nil || strings.Count(string(raw), "run") < 1 {
		t.Fatalf("scan never ran: %q %v", raw, err)
	}
}

// A default build has no tunnel; run must say so instead of idling.
func TestRunWithoutTunnelBuildIsExplicit(t *testing.T) {
	if openTunnel != nil {
		t.Skip("tunnel compiled in")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(filepath.Join(root, "etc/node.json"), &Config{
		Version: configVersion, NodeID: testNodeID, Name: "n", PublicURL: "https://ops.example.com",
		HubAddr: "127.0.0.1:18766", ConsoleAddr: "127.0.0.1:8080", DataDir: filepath.Join(root, "data"),
	}); err != nil {
		t.Fatal(err)
	}
	err := runNode(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "remtunnel") {
		t.Fatalf("run in a default build = %v, want the build-tag error", err)
	}
}
