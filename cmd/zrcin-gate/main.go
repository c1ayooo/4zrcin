// Command zrcin-gate is the single local gate for 4zrcin.
//
// It is the one place the checks live. scripts/gate.ps1 and scripts/gate.sh are
// thin entries that locate the repository root and run this command, so Windows
// and Linux execute the same list against the same implementation — one platform
// cannot quietly lose a check (taskbook P0-1: "单一检查实现 + 两个薄入口").
//
// The steps run serially and each one prints its elapsed time:
//
//  1. go build ./...
//  2. go vet ./...
//  3. go test ./... -count=1 (plus -test-flags, default -timeout 20m)
//     3b. PostgreSQL contract tests: the storage suite again with
//     ZRCIN_TEST_PG_DSN (docs/17 §3); without a database the gate fails with
//     setup guidance, -skip-pg records a skip (unless -skip-pg)
//  4. frontend: pnpm --dir web typecheck / test / build (unless -skip-web)
//  5. embedded-artifact freshness: web/dist must not gain differences
//     the rebuild did not have before; a pre-existing dirty tree is not a failure
//  6. node scripts/check-docs.mjs
//  7. harness: build cyber-harness + cyber-scan from the independent checkout
//     (unless -skip-harness)
//  8. control plane: build bin/zrcin and bin/zrcin-repair, plus a noconsole
//     smoke build of bin/zrcin
//  9. inprochost unit tests: go test -tags <zrcin tags> ./cmd/zrcin
//     ./internal/host/... — the tagged code never compiles into the default
//     test run, so without this step the embedded build ships untested;
//     skipped, with the reason recorded, when no harness checkout exists
// 10. build manifest (.build/build-manifest.json) — source, binaries, versions,
//     platform/toolchain, and the real upstream model id when probing is on
//
// -manifest-only writes the manifest without running any check or build.
//
// The gate fails on the first failing step: a check that did not run has no
// result to report, and the embedded-artifact comparison is only meaningful when
// the frontend build before it succeeded.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/buildinfo"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// goproxyDefault is the fallback module proxy for builds that fetch modules:
// a fresh machine has no module cache yet, and the project documents this
// mirror as its default. An operator-set GOPROXY always wins.
const goproxyDefault = "https://goproxy.cn,direct"

// config is the resolved invocation.
type config struct {
	skipWeb        bool
	skipHarness    bool
	skipPG         bool
	harnessDir     string
	binDir         string
	testFlags      string
	manifestOut    string
	manifestOnly   bool
	modelProbe     bool
	noModelProbe   bool
	modelProbeWait time.Duration
	harnessConfig  string
}

func main() {
	var cfg config
	flag.BoolVar(&cfg.skipWeb, "skip-web", false, "显式跳过前端检查（typecheck/test/build 与嵌入产物新鲜度）；默认不跳过")
	flag.BoolVar(&cfg.skipHarness, "skip-harness", false, "显式跳过 harness 构建；默认不跳过，源码缺失时失败并给出 clone 指引")
	flag.BoolVar(&cfg.skipPG, "skip-pg", false, "显式跳过 PostgreSQL 契约测试；默认不跳过，缺 ZRCIN_TEST_PG_DSN 时失败并给出建库指引（scripts/test-pg.sh）")
	flag.StringVar(&cfg.harnessDir, "harness-dir", "", "cyber-harness 源码目录（默认依次：$HARNESS_DIR、.build/harness-src、同父目录的 cyber-harness）")
	flag.StringVar(&cfg.binDir, "bin", "bin", "二进制输出目录（harness 与 zrcin/zrcin-repair 共用）")
	flag.StringVar(&cfg.testFlags, "test-flags", "-p 1 -timeout 20m", "透传给 go test 的参数（默认 \"-p 1 -timeout 20m\"：串行执行，避免收尾竞态互相淹没）")
	flag.StringVar(&cfg.manifestOut, "manifest-out", buildinfo.ManifestPath, "构建清单输出路径")
	flag.BoolVar(&cfg.manifestOnly, "manifest-only", false, "只生成构建清单，不执行任何检查与构建")
	flag.BoolVar(&cfg.modelProbe, "model-probe", true, "对 active provider 发一次极小请求以记录真实 model id（默认开启；配置缺失或探测失败不使 gate 失败）")
	flag.BoolVar(&cfg.noModelProbe, "no-model-probe", false, "关闭模型探测（与 -model-probe=false 等价）")
	flag.DurationVar(&cfg.modelProbeWait, "model-probe-timeout", 30*time.Second, "模型探测超时")
	flag.StringVar(&cfg.harnessConfig, "harness-config", "", "harness 配置文件（默认 $ZRCIN_HARNESS_CONFIG 或 ~/.cyber/cyber.yaml）")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "zrcin-gate: 无法识别的参数：%v\n", flag.Args())
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "\nzrcin-gate: %v\n", err)
		os.Exit(1)
	}
}

// skipStep marks an optional check whose prerequisite is absent. The gate
// records it as skipped with the reason instead of failing the run; a failure
// must always mean a check that ran and found a problem.
type skipStep struct{ reason string }

func (s skipStep) Error() string { return s.reason }

// step is one serial check. Closures keep the numbering honest when optional
// groups are skipped.
type step struct {
	name string
	run  func() error
}

// run executes the gate.
func run(cfg config) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	exe := buildinfo.ExecutableSuffix()
	goos, goarch := buildinfo.TargetPlatform()
	probe := cfg.modelProbe && !cfg.noModelProbe

	started := time.Now()
	fmt.Printf("zrcin-gate: 仓库 %s\n", root)
	fmt.Printf("zrcin-gate: 目标平台 %s/%s；跳过前端=%v 跳过 harness=%v 跳过 PG 契约=%v 模型探测=%v\n",
		goos, goarch, cfg.skipWeb, cfg.skipHarness, cfg.skipPG, probe)
	if cfg.manifestOnly {
		return writeManifestOnly(cfg, root)
	}

	if err := preflight(cfg, root); err != nil {
		return err
	}

	binDirAbs := absFrom(root, cfg.binDir)
	if err := os.MkdirAll(binDirAbs, 0o755); err != nil {
		return fmt.Errorf("创建二进制目录 %s：%w", binDirAbs, err)
	}

	harness := buildinfo.ResolveHarnessSource(root, cfg.harnessDir)
	// The inprochost build imports the fork through go.mod's replace => the
	// checkout; it is only buildable when that checkout exists.
	zrcinTags := ""
	if harness.Found {
		zrcinTags = "inprochost"
	}
	// The remtunnel build imports the sibling rem checkout through go.mod's
	// replace => ../rem; like inprochost it only builds when that checkout is
	// present, and the default build graph stays free of it.
	if st, err := os.Stat(filepath.Join(filepath.Dir(root), "rem")); err == nil && st.IsDir() {
		if zrcinTags != "" {
			zrcinTags += ","
		}
		zrcinTags += "remtunnel"
	}
	var embedded *buildinfo.EmbeddedArtifacts

	steps := []step{
		{name: "go build ./...", run: func() error {
			return runCommand(root, nil, "go", "build", "./...")
		}},
		{name: "go vet ./...", run: func() error {
			return runCommand(root, nil, "go", "vet", "./...")
		}},
		{name: "gofmt -l cmd internal", run: func() error {
			// Formatting is a gate check, not a suggestion: a drifted file means
			// the next real edit carries format noise in its diff. gofmt -l exits
			// 0 even when it lists files, so the listed output decides.
			cmd := exec.Command("gofmt", "-l", "cmd", "internal")
			cmd.Dir = root
			out, err := cmd.Output()
			if err != nil {
				return err
			}
			if drifted := strings.Fields(string(out)); len(drifted) > 0 {
				return fmt.Errorf("以下文件存在 gofmt 漂移（运行 gofmt -w cmd internal 修复）：\n%s",
					strings.Join(drifted, "\n"))
			}
			return nil
		}},
		{name: "go test ./... -count=1 " + strings.TrimSpace(cfg.testFlags), run: func() error {
			args := []string{"test", "-count=1"}
			args = append(args, strings.Fields(cfg.testFlags)...)
			args = append(args, "./...")
			return runCommand(root, nil, "go", args...)
		}},
		pgContractStep(cfg, root),
	}
	// The web sources live in the 4zweb checkout (docs/19 P1-3). When it is
	// present the gate rebuilds and compares; when it is absent the console
	// bundle is taken as committed and the web steps are recorded as skipped
	// — typecheck/test/build belong to 4zweb's own CI.
	webDir := resolveWebSource(root)
	if !cfg.skipWeb && webDir == "" {
		steps = append(steps, step{name: "web: 跳过（未找到 4zweb 检出：设 ZRCIN_WEB_DIR 或在同父目录检出）", run: func() error {
			return nil
		}})
	} else if !cfg.skipWeb {
		wd := webDir
		steps = append(steps,
			step{name: "web: pnpm typecheck", run: func() error {
				return runCommand(wd, nil, "pnpm", "typecheck")
			}},
			step{name: "web: pnpm test", run: func() error {
				return runCommand(wd, nil, "pnpm", "test")
			}},
			step{name: "web: pnpm build + go generate + 嵌入产物新鲜度", run: func() error {
				artifacts, err := buildinfo.CheckEmbeddedFreshness(root, func() error {
					if err := runCommand(wd, nil, "pnpm", "build"); err != nil {
						return err
					}
					// Regenerate the console bundle from the fresh dist; the
					// snapshot comparison around this function fails the gate
					// when the committed bundle was stale (docs/04 §4.1).
					return runCommand(root, nil, "go", "generate", "./internal/web")
				})
				if err != nil {
					return err
				}
				embedded = artifacts
				if len(artifacts.Changed) > 0 {
					return fmt.Errorf("嵌入产物落后于前端源码：本次 pnpm build 让 %s 产生了新差异。"+
						"请把重新构建的 %s 一并提交。变化文件：%s",
						artifacts.Dir, artifacts.Dir, buildinfo.FormatChangedPaths(artifacts.Changed, 20))
				}
				return nil
			}},
		)
	}
	steps = append(steps, step{name: "node scripts/check-docs.mjs", run: func() error {
		return runCommand(root, nil, "node", "scripts/check-docs.mjs")
	}})

	if !cfg.skipHarness {
		steps = append(steps, step{name: "harness: 构建 cyber-harness + cyber-scan", run: func() error {
			if !harness.Found {
				return errors.New(buildinfo.HarnessCloneHint(root))
			}
			return buildHarness(harness.Dir, binDirAbs)
		}})
	} else {
		fmt.Printf("zrcin-gate: 跳过 harness 构建（-skip-harness）；清单仍会记录已有二进制\n")
	}

	steps = append(steps,
		step{name: "go build bin/zrcin + bin/zrcin-repair", run: func() error {
			buildArgs := []string{"build"}
			if zrcinTags != "" {
				buildArgs = append(buildArgs, "-tags", zrcinTags)
			}
			buildArgs = append(buildArgs, "-o", filepath.Join(binDirAbs, "zrcin"+exe), "./cmd/zrcin")
			if err := runCommand(root, nil, "go", buildArgs...); err != nil {
				return err
			}
			if err := runCommand(root, nil, "go", "build", "-o", filepath.Join(binDirAbs, "zrcin-repair"+exe), "./cmd/zrcin-repair"); err != nil {
				return err
			}
			// The launcher is the node-side artifact the ingress hands out
			// (docs/14 §7.1): build it whenever the tunnel is in the binary.
			if zrcinTags != "" && containsTag(zrcinTags, "remtunnel") {
				return runCommand(root, nil, "go", "build", "-tags", "remtunnel", "-o", filepath.Join(binDirAbs, "zrcin-node"+exe), "./cmd/zrcin-node")
			}
			return nil
		}},
		func() step {
			smokeArgs := []string{"build"}
			if zrcinTags != "" {
				smokeArgs = append(smokeArgs, "-tags", zrcinTags+",noconsole")
			} else {
				smokeArgs = append(smokeArgs, "-tags", "noconsole")
			}
			smokeArgs = append(smokeArgs, "-o", filepath.Join(binDirAbs, "zrcin-noconsole-smoke"+exe), "./cmd/zrcin")
			return step{name: "noconsole smoke build", run: func() error {
				// The noconsole form must keep compiling: test binaries and
				// UI-less node builds carry the stub, not the bundle (docs/04
				// §3.4). With a fork checkout the smoke also proves the
				// inproc×noconsole combination. The artifact is deleted so
				// bin/ never accumulates a second zrcin.
				smoke := filepath.Join(binDirAbs, "zrcin-noconsole-smoke"+exe)
				if err := runCommand(root, nil, "go", smokeArgs...); err != nil {
					return err
				}
				return os.Remove(smoke)
			}}
		}(),
		// The embedded build's unit tests run here, with the same tags the
		// build above used: the default `go test ./...` never compiles the
		// inprochost code, so without this step the self-node path ships
		// untested (docs/19 P6). No harness checkout means no tagged code and
		// nothing to test — recorded as a skip, not a silent pass. The tests
		// themselves are unit-level: no model, no network, no engine build.
		func() step {
			if zrcinTags == "" {
				return step{name: "go test -tags inprochost ./cmd/zrcin ./internal/host/...", run: func() error {
					return skipStep{reason: "没有 harness 检出：本机构建不带 inprochost，内嵌代码没有可测单元"}
				}}
			}
			return step{name: "go test -tags " + zrcinTags + " ./cmd/zrcin ./internal/host/...", run: func() error {
				return runCommand(root, nil, "go", "test", "-count=1", "-tags", zrcinTags, "./cmd/zrcin", "./internal/host/...")
			}}
		}(),
	)

	checks := make([]buildinfo.Check, 0, len(steps))
	for i, s := range steps {
		fmt.Printf("[%d/%d] %s ... ", i+1, len(steps)+1, s.name)
		start := time.Now()
		err := s.run()
		elapsed := time.Since(start)
		if err != nil {
			var skip skipStep
			if errors.As(err, &skip) {
				fmt.Printf("跳过（%s；%s）\n", skip.reason, formatDuration(elapsed))
				checks = append(checks, buildinfo.Check{
					Name:       s.name,
					DurationMS: elapsed.Milliseconds(),
					Skipped:    true,
					SkipReason: skip.reason,
				})
				continue
			}
			fmt.Printf("失败（%s）\n", formatDuration(elapsed))
			return fmt.Errorf("%s：%w", s.name, err)
		}
		fmt.Printf("ok（%s）\n", formatDuration(elapsed))
		checks = append(checks, buildinfo.Check{Name: s.name, DurationMS: elapsed.Milliseconds()})
	}

	// The manifest is the last step: it hashes the binaries the previous steps
	// just built and reads the model connection the run would use.
	fmt.Printf("[%d/%d] 生成构建清单 %s ... ", len(steps)+1, len(steps)+1, cfg.manifestOut)
	manifestStart := time.Now()
	gateInfo := &buildinfo.GateInfo{
		StartedAt: started.UTC().Format(time.RFC3339),
		Skips:     buildinfo.Skips{Web: cfg.skipWeb, Harness: cfg.skipHarness, PG: cfg.skipPG},
		Checks:    checks,
		Embedded:  embedded,
	}
	out := absFrom(root, cfg.manifestOut)
	manifest, err := buildinfo.Collect(context.Background(), buildinfo.Options{
		Root:          root,
		HarnessDir:    harness.Dir,
		BinDir:        cfg.binDir,
		HarnessConfig: cfg.harnessConfig,
		ProbeModel:    probe,
		ProbeTimeout:  cfg.modelProbeWait,
		Gate:          gateInfo,
	})
	if err != nil {
		fmt.Printf("失败（%s）\n", formatDuration(time.Since(manifestStart)))
		return fmt.Errorf("生成构建清单：%w", err)
	}
	if err := buildinfo.Write(out, manifest); err != nil {
		fmt.Printf("失败（%s）\n", formatDuration(time.Since(manifestStart)))
		return fmt.Errorf("写入构建清单：%w", err)
	}
	fmt.Printf("ok（%s）\n", formatDuration(time.Since(manifestStart)))

	fmt.Printf("\nzrcin-gate: 全部检查通过，用时 %s\n\n%s", formatDuration(time.Since(started)),
		buildinfo.Describe(manifest, buildinfo.ManifestRelPath(root, out)))
	return nil
}

// writeManifestOnly implements -manifest-only.
func writeManifestOnly(cfg config, root string) error {
	harness := buildinfo.ResolveHarnessSource(root, cfg.harnessDir)
	out := absFrom(root, cfg.manifestOut)
	probe := cfg.modelProbe && !cfg.noModelProbe
	manifest, err := buildinfo.Collect(context.Background(), buildinfo.Options{
		Root:          root,
		HarnessDir:    harness.Dir,
		BinDir:        cfg.binDir,
		HarnessConfig: cfg.harnessConfig,
		ProbeModel:    probe,
		ProbeTimeout:  cfg.modelProbeWait,
	})
	if err != nil {
		return fmt.Errorf("生成构建清单：%w", err)
	}
	if err := buildinfo.Write(out, manifest); err != nil {
		return fmt.Errorf("写入构建清单：%w", err)
	}
	fmt.Printf("\n%s", buildinfo.Describe(manifest, buildinfo.ManifestRelPath(root, out)))
	return nil
}

// preflight fails early with an actionable message when a prerequisite is
// missing. It never skips a check: a missing prerequisite is an error.
func preflight(cfg config, root string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("找不到 go：需要 Go 1.26+（Windows: winget install GoLang.Go 或 https://go.dev/dl/；Debian/Ubuntu: sudo apt-get install -y golang-go 或官方 tarball），装好后重开终端")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("找不到 git：清单需要 git 记录源码身份（Windows 用 Git for Windows，Linux 用发行版 git）")
	}
	if err := requireVersionCommand("node", "找不到 node：前端检查与 scripts/check-docs.mjs 需要 Node.js 24+（Windows: 官方安装包或 winget install OpenJS.NodeJS.LTS；Debian/Ubuntu: 发行版包或官方 Node 24 tarball 解压后把 bin 加入 PATH）"); err != nil {
		return err
	}
	if !cfg.skipWeb && resolveWebSource(root) != "" {
		if err := requireVersionCommand("pnpm", "找不到 pnpm：先装 Node.js 24+，再 npm i -g pnpm（或 corepack enable）；Windows 与 Linux 各自安装本平台的依赖"); err != nil {
			return err
		}
	}
	return nil
}

// requireVersionCommand checks that a tool not only exists on PATH but actually
// runs. On WSL a Windows shim can be first on PATH and still fail with its own
// "node not found"; resolving that here gives a clear message instead of a
// confusing failure inside a later step.
func requireVersionCommand(name, missingHint string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return errors.New(missingHint)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s 不可用（%s）：%s\n%s", name, path, strings.TrimSpace(string(out)), missingHint)
	}
	return nil
}

// containsTag reports whether the comma-joined tag list carries name.
func containsTag(tags, name string) bool {
	for _, tag := range strings.Split(tags, ",") {
		if strings.TrimSpace(tag) == name {
			return true
		}
	}
	return false
}

// harnessMakeSpec assembles the make invocation that forwards the harness
// build to the engine checkout's own Makefile (docs/19 §7 build convergence):
// the engine owns its tag set now — `engine` builds cyber-harness with the
// full set, `scan` builds cyber-scan without `full` — so the shell no longer
// assembles tags here. goProxy is the environment's GOPROXY; blank means the
// default mirror is supplied so a fresh machine can still fetch.
func harnessMakeSpec(engineDir, binDir, goProxy string) (argv, env []string) {
	argv = []string{"make", "-C", engineDir, "engine", "scan", "BIN_DIR=" + binDir}
	if strings.TrimSpace(goProxy) == "" {
		env = append(env, "GOPROXY="+goproxyDefault)
	}
	return argv, env
}

// buildHarness forwards the Hub and scan builds to the engine checkout's own
// Makefile. The step keeps its name and its products (bin/cyber-harness,
// bin/cyber-scan for the manifest); only the invocation changed — the tags
// live in the engine Makefile now (docs/19 §7).
func buildHarness(engineDir, binDir string) error {
	if _, err := exec.LookPath("make"); err != nil {
		return errors.New("找不到 make：harness 构建已转发给引擎自己的 Makefile，请先安装 make 再跑 gate")
	}
	argv, env := harnessMakeSpec(engineDir, binDir, os.Getenv("GOPROXY"))
	if err := runCommand(engineDir, env, argv[0], argv[1:]...); err != nil {
		return fmt.Errorf("构建 cyber-harness + cyber-scan：%w", err)
	}
	return nil
}

// runCommand runs one child with inherited stdio so its output streams into the
// gate log (and any CI-like wrapper) as it happens.
func runCommand(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		if cmd.ProcessState != nil {
			return fmt.Errorf("%s 退出码 %d：%w", strings.Join(append([]string{name}, args...), " "), cmd.ProcessState.ExitCode(), err)
		}
		return err
	}
	return nil
}

// repoRoot resolves the checkout root. The scripts cd here first; a manual
// `go run ./cmd/zrcin-gate` from a subdirectory is still tolerated through git.
func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	if out, err := cmd.Output(); err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			return root, nil
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return cwd, nil
	}
	return "", errors.New("无法定位 4zrcin 仓库根：请在仓库根目录运行（scripts/gate.ps1 与 scripts/gate.sh 会自动定位）")
}

func absFrom(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// pgContractStep runs the storage suite a second time against PostgreSQL
// (docs/16 §13 M0, docs/17 §3). The server version goes into the step name so
// the build manifest records whether the contract ran and against which
// server. No database is a failure with setup guidance, not a silent skip;
// -skip-pg is the explicit, recorded opt-out.
func pgContractStep(cfg config, root string) step {
	name := "PostgreSQL 契约测试（internal/store 双后端）"
	dsn := strings.TrimSpace(os.Getenv("ZRCIN_TEST_PG_DSN"))
	if dsn != "" {
		if db, err := sql.Open("pgx", dsn); err == nil {
			var version string
			if err := db.QueryRow("SHOW server_version").Scan(&version); err == nil {
				name += fmt.Sprintf("（server %s）", strings.Fields(version)[0])
			}
			_ = db.Close()
		}
	}
	return step{name: name, run: func() error {
		if cfg.skipPG {
			return skipStep{reason: "-skip-pg"}
		}
		if dsn == "" {
			return errors.New("缺 ZRCIN_TEST_PG_DSN：PostgreSQL 契约测试需要一个测试库。" +
				"有 Docker 时先运行 `export ZRCIN_TEST_PG_DSN=$(bash scripts/test-pg.sh)`（一次性容器，--stop 停止）；" +
				"没有 Docker 就安装本机 PostgreSQL 16+（Debian/Ubuntu: sudo apt-get install -y postgresql；" +
				"Windows: 官方安装包），在库上 CREATE EXTENSION pg_trgm 后导出连接串。" +
				"确要跳过用 -skip-pg，跳过会记入构建清单")
		}
		return runCommand(root, []string{"ZRCIN_TEST_PG_DSN=" + dsn}, "go", "test", "-count=1", "./internal/store/")
	}}
}

// resolveWebSource locates the 4zweb checkout: $ZRCIN_WEB_DIR, then the
// sibling 4zweb beside this tree (the layout the go.mod replace convention
// uses for the engine). Empty means "not found" — the web steps skip and the
// committed console bundle stands.
func resolveWebSource(root string) string {
	if env := strings.TrimSpace(os.Getenv("ZRCIN_WEB_DIR")); env != "" {
		if info, err := os.Stat(filepath.Join(env, "package.json")); err == nil && !info.IsDir() {
			return env
		}
		return ""
	}
	sibling := filepath.Join(filepath.Dir(root), "4zweb")
	if info, err := os.Stat(filepath.Join(sibling, "package.json")); err == nil && !info.IsDir() {
		return sibling
	}
	return ""
}
