package enroll

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Artifact names the node package carries (docs/14 §7.1).
const (
	ArtLauncher = "zrcin-node"
	ArtScanFull = "cyber-scan"
	ArtCLI      = "zrcin"
)

// DistConfig locates the artifacts this platform can serve. The launcher and
// the scan binaries are same-platform builds that sit beside a running zrcin;
// other platforms come from <data>/node-dist/<os>-<arch>/ (docs/14 §7.1).
type DistConfig struct {
	// ExePath is the running zrcin binary; the launcher and the scan default
	// to its directory.
	ExePath string
	// ScanPath overrides the scan binary location (the --scan flag).
	ScanPath string
	// DataDir roots the per-platform artifact directory.
	DataDir string
	// Paths pins individual artifacts (tests and unusual deployments).
	Paths map[string]string
}

// DistStatus is the operator-facing "what can this platform hand out" answer.
// Missing artifacts are normal (a platform built without the launcher yet must
// still serve the console); the status says so instead of failing.
type DistStatus struct {
	Launcher bool `json:"launcher"`
	ScanFull bool `json:"scan_full"`
	// ScanCapture says whether the console may offer the capture deployment.
	// 4zweb enables that option off this flag; the capture node is the same
	// cyber-scan binary launched with --capture-only (docs/19 P6), so it is
	// available exactly when that binary is. Reporting false here (as the first
	// cut of P6 did) greyed the option out for good.
	ScanCapture bool `json:"scan_capture"`
}

// Dist serves the node artifacts and the bootstrap script. Digests are cached
// per path+mtime+size: an artifact swap changes the mtime and rehashes.
type Dist struct {
	cfg DistConfig

	mu    sync.Mutex
	cache map[string]cachedArtifact
}

type cachedArtifact struct {
	path  string
	size  int64
	mtime int64
	sum   string
}

type artifact struct {
	Name string
	Path string
	Sum  string
	Size int64
}

// NewDist builds the artifact source.
func NewDist(cfg DistConfig) *Dist {
	return &Dist{cfg: cfg, cache: map[string]cachedArtifact{}}
}

// platformDir is where a non-native platform's artifacts live.
func (d *Dist) platformDir(goos, goarch string) string {
	return filepath.Join(d.cfg.DataDir, "node-dist", goos+"-"+goarch)
}

// resolve finds one artifact for the requested platform. Native artifacts come
// from beside zrcin; other platforms from the per-platform directory.
func (d *Dist) resolve(name, goos, goarch string) (artifact, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return artifact{}, errors.New("缺少产物名")
	}
	if d.cfg.Paths != nil {
		if p := d.cfg.Paths[name]; p != "" {
			return d.artifact(name, p)
		}
	}
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		if p := d.nativePath(name); p != "" {
			if a, err := d.artifact(name, p); err == nil {
				return a, nil
			}
		}
	}
	candidate := filepath.Join(d.platformDir(goos, goarch), exeName(name))
	if a, err := d.artifact(name, candidate); err == nil {
		return a, nil
	}
	return artifact{}, fmt.Errorf("平台没有 %s/%s 的 %s 产物", goos, goarch, name)
}

// nativePath picks the same-platform location of an artifact.
func (d *Dist) nativePath(name string) string {
	exeDir := ""
	if d.cfg.ExePath != "" {
		exeDir = filepath.Dir(d.cfg.ExePath)
	}
	switch name {
	case ArtLauncher:
		return firstExisting(exeName(filepath.Join(exeDir, ArtLauncher)))
	case ArtScanFull:
		if d.cfg.ScanPath != "" {
			return d.cfg.ScanPath
		}
		return firstExisting(exeName(filepath.Join(exeDir, ArtScanFull)))
	case ArtCLI:
		return d.cfg.ExePath
	}
	return ""
}

// exeName appends the platform executable suffix when the caller omitted it.
func exeName(p string) string {
	if runtime.GOOS != "windows" || p == "" || strings.HasSuffix(strings.ToLower(p), ".exe") {
		return p
	}
	return p + ".exe"
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// artifact hashes one file, reusing the cache when the file is unchanged.
func (d *Dist) artifact(name, path string) (artifact, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return artifact{}, fmt.Errorf("找不到 %s：%s", name, path)
	}
	d.mu.Lock()
	cached, ok := d.cache[name]
	d.mu.Unlock()
	if ok && cached.path == path && cached.size == st.Size() && cached.mtime == st.ModTime().UnixNano() {
		return artifact{Name: name, Path: path, Sum: cached.sum, Size: cached.size}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return artifact{}, fmt.Errorf("读取 %s：%w", name, err)
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return artifact{}, fmt.Errorf("读取 %s：%w", name, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	d.mu.Lock()
	d.cache[name] = cachedArtifact{path: path, size: st.Size(), mtime: st.ModTime().UnixNano(), sum: sum}
	d.mu.Unlock()
	return artifact{Name: name, Path: path, Sum: sum, Size: st.Size()}, nil
}

// Status reports which artifacts this platform can currently serve.
func (d *Dist) Status() DistStatus {
	ok := func(name string) bool {
		_, err := d.resolve(name, runtime.GOOS, runtime.GOARCH)
		return err == nil
	}
	scan := ok(ArtScanFull)
	return DistStatus{Launcher: ok(ArtLauncher), ScanFull: scan, ScanCapture: scan}
}

// artifactFilename is the name the download carries.
func artifactFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// RegisterRoutes mounts the distribution surface on the node ingress
// (docs/14 §7.1): the bootstrap script and the launcher download authenticate
// with the enrollment token, the scan/CLI downloads with the node credential.
func (d *Dist) RegisterRoutes(ing *Ingress, svc *Service) {
	ing.RegisterPrefix("/join/", d.joinHandler(svc))
	ing.RegisterPrefix("/node/dist/", d.nodeDistHandler(svc))
}

// joinHandler serves the per-enrollment routes:
//
//	GET /join/{token}/install.sh
//	GET /join/{token}/dist/zrcin-node?os=&arch=
//	GET /join/{token}/dist/zrcin-node.sha256?os=&arch=
func (d *Dist) joinHandler(svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "只接受 GET")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/join/")
		token, tail, ok := strings.Cut(rest, "/")
		if !ok || token == "" {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if _, valid := svc.CheckToken(token); !valid {
			writeErr(w, http.StatusGone, "接入令牌已作废")
			return
		}
		switch tail {
		case "install.sh":
			d.serveBootstrap(w, svc, token)
		case "dist/zrcin-node":
			d.serveArtifact(w, r, ArtLauncher)
		case "dist/zrcin-node.sha256":
			d.serveDigest(w, r, ArtLauncher)
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	})
}

// nodeDistHandler serves GET /node/dist/{name} to a joined node: the scan
// binary plus the CLI the sessions use. The enrollment recipe no longer picks
// a different artifact — the capture recipe is the same binary launched with
// --capture-only (docs/19 P6) — so every node gets cyber-scan here.
func (d *Dist) nodeDistHandler(svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HEAD is the repair install's "did this artifact change?": the same
		// fenced answer with the digest and no body (ServeContent sends none).
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "只接受 GET 与 HEAD")
			return
		}
		presented, ok := bearer(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "缺少节点凭据")
			return
		}
		view, ok := svc.AuthenticateNode(presented)
		if !ok {
			status := http.StatusUnauthorized
			if view.State == NodeDestroyed {
				status = http.StatusGone
			}
			writeErr(w, status, "节点凭据无效")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/node/dist/")
		switch name {
		case ArtScanFull:
			// Served as-is: the recipe travels through the node config and the
			// launcher adds --capture-only at start, it does not swap bytes.
		case ArtCLI:
			// served as-is
		default:
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		d.serveArtifact(w, r, name)
	})
}

func (d *Dist) serveArtifact(w http.ResponseWriter, r *http.Request, name string) {
	goos, goarch := platformQuery(r)
	a, err := d.resolve(name, goos, goarch)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error()+"；把产物放到 "+d.platformDir(goos, goarch)+" 后重试")
		return
	}
	f, err := os.Open(a.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取产物失败")
		return
	}
	defer f.Close() //nolint:errcheck
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取产物失败")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, artifactFilename(name)))
	w.Header().Set("X-Checksum-Sha256", a.Sum)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, artifactFilename(name), st.ModTime(), f)
}

func (d *Dist) serveDigest(w http.ResponseWriter, r *http.Request, name string) {
	goos, goarch := platformQuery(r)
	a, err := d.resolve(name, goos, goarch)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, a.Sum)
}

// platformQuery reads ?os=&arch=, defaulting to the platform itself (a
// request without them means "the platform's own build").
func platformQuery(r *http.Request) (goos, goarch string) {
	goos = strings.TrimSpace(r.URL.Query().Get("os"))
	goarch = strings.TrimSpace(r.URL.Query().Get("arch"))
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// Timeout: the bootstrap script must not hang forever on a dead platform. It
// must also fit a slow path: the launcher is 13 MB, and through a tunnel relay
// behind a home uplink that is more than a minute — a cut download reads as a
// broken platform to an operator who only ran the one-liner.
const (
	bootstrapDownloadTimeout = 300
)

// bootstrapScript is the one-liner's whole content (docs/14 §7): identify
// the platform, download the launcher, verify its digest, run install. All
// real work lives in the launcher; this script is only the bootstrap.
const bootstrapScript = `#!/bin/sh
# 4zrcin 节点引导：下载启动器并校验 SHA-256，然后交给它安装。
# 由平台按接入令牌生成；令牌首次回连后即作废。
set -eu
BASE=%q
TOKEN=%q
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT INT TERM

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "不支持的系统：$os（首批只支持 Linux，含 WSL2）" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "不支持的架构：$arch" >&2; exit 1 ;;
esac

dl="$BASE/join/$TOKEN/dist/zrcin-node?os=$os&arch=$arch"
want=$(curl -fsSL --max-time %d "$BASE/join/$TOKEN/dist/zrcin-node.sha256?os=$os&arch=$arch") || {
  echo "平台没有 $os/$arch 的启动器产物；确认启动器已随 zrcin 一起构建" >&2
  exit 1
}
curl -fsSL --max-time %d -o "$TMP" "$dl"
echo "$want  $TMP" | sha256sum -c - >/dev/null || {
  echo "启动器校验失败：下载被篡改或产物已更新，请重新生成接入命令" >&2
  exit 1
}
chmod 755 "$TMP"
exec "$TMP" install --join "$BASE/join/$TOKEN"
`

// Script renders the bootstrap script for one enrollment token (docs/14 §7).
// The HTTP join route serves these bytes; the SSH deploy path pipes the same
// script to the remote shell's stdin, so there is one bootstrap to keep right.
func (d *Dist) Script(base, token string) (string, error) {
	if _, err := d.resolve(ArtLauncher, runtime.GOOS, runtime.GOARCH); err != nil {
		return "", fmt.Errorf("平台还没有启动器产物（zrcin-node）：先随 zrcin 一起构建 bin/zrcin-node，或放到 %s",
			d.platformDir(runtime.GOOS, runtime.GOARCH))
	}
	return fmt.Sprintf(bootstrapScript, strings.TrimRight(base, "/"), token,
		bootstrapDownloadTimeout, bootstrapDownloadTimeout), nil
}

// serveBootstrap renders the script for one enrollment token. The digest is
// fetched by the script itself (a separate endpoint), so the script never goes
// stale against a rebuilt launcher.
func (d *Dist) serveBootstrap(w http.ResponseWriter, svc *Service, token string) {
	script, err := d.Script(svc.PublicURL(), token)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, script)
}
