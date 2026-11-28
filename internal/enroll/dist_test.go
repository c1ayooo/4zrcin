package enroll

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The distribution surface (docs/14 §7.1, §7.1): the bootstrap script, the
// launcher, and the per-recipe scan/CLI downloads. What is missing must be
// reported honestly, never served as an empty body.

func writeFakeArtifact(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newDistFixture(t *testing.T) (*Dist, *Service, *Ingress, string, string) {
	t.Helper()
	artDir := t.TempDir()
	launcher := writeFakeArtifact(t, artDir, "zrcin-node", "fake-launcher-bytes")
	fullScan := writeFakeArtifact(t, artDir, "cyber-scan", "fake-full-scan")
	cli := writeFakeArtifact(t, artDir, "zrcin", "fake-cli")

	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{Recipe: RecipeCapture})
	joined := mustJoin(t, svc, created.Token, "install-1")

	dist := NewDist(DistConfig{
		DataDir: t.TempDir(),
		Paths: map[string]string{
			ArtLauncher: launcher,
			ArtScanFull: fullScan,
			ArtCLI:      cli,
		},
	})
	ing := NewIngress(svc)
	dist.RegisterRoutes(ing, svc)
	return dist, svc, ing, created.Token, joined.NodeCredential
}

func doIngress(t *testing.T, ing *Ingress, method, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "192.0.2.20:4444"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	ing.ServeHTTP(rec, req)
	return rec
}

func TestDistStatusReflectsArtifacts(t *testing.T) {
	dist, _, _, _, _ := newDistFixture(t)
	got := dist.Status()
	if !got.Launcher || !got.ScanFull {
		t.Fatalf("status with all artifacts = %+v, want launcher+scan true", got)
	}
	// The console enables its capture deployment off scan_capture: the
	// capture node is the same cyber-scan binary launched with --capture-only
	// (docs/19 P6), so the option is available exactly when that binary is.
	if !got.ScanCapture {
		t.Fatalf("status scan_capture = false: the console would grey out the capture deployment")
	}
	empty := NewDist(DistConfig{DataDir: t.TempDir()})
	if got := empty.Status(); got.Launcher || got.ScanFull || got.ScanCapture {
		t.Fatalf("status without artifacts = %+v, want all false", got)
	}
}

func TestBootstrapScriptAndLauncherDownload(t *testing.T) {
	_, _, ing, token, _ := newDistFixture(t)

	rec := doIngress(t, ing, http.MethodGet, "/join/"+token+"/install.sh", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("install.sh = %d %s", rec.Code, rec.Body)
	}
	script := rec.Body.String()
	for _, want := range []string{"#!/bin/sh", "BASE=", "/join/$TOKEN/dist/zrcin-node", "sha256sum -c", "install --join"} {
		if !strings.Contains(script, want) {
			t.Fatalf("install.sh lacks %q:\n%s", want, script)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Fatalf("install.sh content-type = %q", ct)
	}

	rec = doIngress(t, ing, http.MethodGet, "/join/"+token+"/dist/zrcin-node", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-launcher-bytes" {
		t.Fatalf("launcher download = %d %q", rec.Code, rec.Body.String())
	}
	sum := sha256.Sum256([]byte("fake-launcher-bytes"))
	wantSum := hex.EncodeToString(sum[:])
	if got := rec.Header().Get("X-Checksum-Sha256"); got != wantSum {
		t.Fatalf("launcher digest header = %q, want %q", got, wantSum)
	}
	rec = doIngress(t, ing, http.MethodGet, "/join/"+token+"/dist/zrcin-node.sha256", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != wantSum {
		t.Fatalf("digest endpoint = %d %q", rec.Code, rec.Body.String())
	}
}

func TestBootstrapRefusesSpentTicketAndMissingLauncher(t *testing.T) {
	_, svc, ing, token, _ := newDistFixture(t)
	svc.MarkJoined("whatever") // no-op; spend the real one via the join path below
	// Spend the token the way the tunnel does.
	views := svc.ListEnrollments()
	if len(views) != 1 {
		t.Fatalf("enrollments = %d", len(views))
	}
	svc.MarkJoined(views[0].NodeID)
	if rec := doIngress(t, ing, http.MethodGet, "/join/"+token+"/install.sh", ""); rec.Code != http.StatusGone {
		t.Fatalf("install.sh with a spent token = %d, want 410", rec.Code)
	}

	// No launcher artifact: the script endpoint says so instead of handing out
	// a script that cannot work.
	svc2, _, _ := newTestService(t)
	created := mustCreate(t, svc2, CreateOptions{})
	dist := NewDist(DistConfig{DataDir: t.TempDir()})
	ing2 := NewIngress(svc2)
	dist.RegisterRoutes(ing2, svc2)
	rec := doIngress(t, ing2, http.MethodGet, "/join/"+created.Token+"/install.sh", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "zrcin-node") {
		t.Fatalf("install.sh without a launcher = %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeDistServesOneScanBinaryAndFencesCredentials(t *testing.T) {
	_, _, ing, _, cred := newDistFixture(t)

	// The capture enrollment gets the same cyber-scan artifact: the recipe is
	// the launcher's --capture-only flag since docs/19 P6, not other bytes.
	rec := doIngress(t, ing, http.MethodGet, "/node/dist/cyber-scan", cred)
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-full-scan" {
		t.Fatalf("scan download for a capture enrollment = %d %q", rec.Code, rec.Body.String())
	}
	rec = doIngress(t, ing, http.MethodGet, "/node/dist/zrcin", cred)
	if rec.Code != http.StatusOK || rec.Body.String() != "fake-cli" {
		t.Fatalf("cli download = %d %q", rec.Code, rec.Body.String())
	}
	if rec := doIngress(t, ing, http.MethodGet, "/node/dist/cyber-scan", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous scan download = %d", rec.Code)
	}
	if rec := doIngress(t, ing, http.MethodGet, "/node/dist/secret", cred); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown artifact = %d", rec.Code)
	}
}

// A platform only carries its own build: asking for another platform without a
// per-platform directory must fail with the place to put it, not a 500.
func TestOtherPlatformArtifactsComeFromTheDistDir(t *testing.T) {
	svc, _, _ := newTestService(t)
	created := mustCreate(t, svc, CreateOptions{})
	joined := mustJoin(t, svc, created.Token, "install-1")
	dataDir := t.TempDir()
	dist := NewDist(DistConfig{DataDir: dataDir})
	ing := NewIngress(svc)
	dist.RegisterRoutes(ing, svc)

	other := "plan9"
	rec := doIngress(t, ing, http.MethodGet, "/node/dist/cyber-scan?os="+other+"&arch=mips", joined.NodeCredential)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), filepath.Join("node-dist", other+"-mips")) {
		t.Fatalf("foreign platform download = %d %s", rec.Code, rec.Body.String())
	}

	// Drop the artifact in the documented place and it is served.
	dir := filepath.Join(dataDir, "node-dist", other+"-mips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cyber-scan"), []byte("plan9-scan"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = doIngress(t, ing, http.MethodGet, "/node/dist/cyber-scan?os="+other+"&arch=mips", joined.NodeCredential)
	if rec.Code != http.StatusOK || rec.Body.String() != "plan9-scan" {
		t.Fatalf("staged foreign platform download = %d %q", rec.Code, rec.Body.String())
	}
	_ = runtime.GOOS
}

// A repair install asks HEAD before it downloads (docs/14 §7.2): the answer
// is the artifact's digest and nothing else, behind the same credential
// fence, so a file that did not change costs no payload through the tunnel.
func TestNodeDistAnswersHeadWithTheDigest(t *testing.T) {
	_, _, ing, _, cred := newDistFixture(t)

	rec := doIngress(t, ing, http.MethodHead, "/node/dist/cyber-scan", cred)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD scan = %d, body %d bytes", rec.Code, rec.Body.Len())
	}
	sum := sha256.Sum256([]byte("fake-full-scan"))
	if got := rec.Header().Get("X-Checksum-Sha256"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("HEAD digest = %q", got)
	}
	if rec := doIngress(t, ing, http.MethodHead, "/node/dist/cyber-scan", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous HEAD = %d", rec.Code)
	}
	if rec := doIngress(t, ing, http.MethodDelete, "/node/dist/cyber-scan", cred); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("other methods = %d", rec.Code)
	}
}
