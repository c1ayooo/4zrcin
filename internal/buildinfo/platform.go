package buildinfo

// Platform and toolchain facts. A missing optional tool (node, pnpm, git) is
// recorded as null plus a reason in Errors: the taskbook requires the manifest to
// say "this machine could not record it", not to fail the gate for a tool the
// Go-only checks do not need.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// executableSuffix is the Go target's executable extension.
func executableSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// TargetPlatform is the GOOS/GOARCH the Go toolchain will build for. The gate
// uses it to pick binary extensions and the harness build tags, so both
// platforms run the identical check list against their own target.
func TargetPlatform() (goos, goarch string) { return targetPlatform() }

// ExecutableSuffix is ".exe" for a Windows target, "" otherwise.
func ExecutableSuffix() string {
	goos, _ := targetPlatform()
	return executableSuffix(goos)
}

// targetPlatform is the GOOS/GOARCH the Go toolchain will build for, so a gate
// run that cross-compiles records the target, not the host. Falls back to the
// running binary when `go env` is unavailable.
func targetPlatform() (goos, goarch string) {
	goos = goEnv("GOOS")
	goarch = goEnv("GOARCH")
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// goEnv reads one `go env` value; "" when go is missing or the query fails.
func goEnv(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "env", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

// collectPlatform records the target plus the toolchain versions. All command
// probes are best-effort: a failure lands in Errors, the field stays null.
func collectPlatform(goos, goarch string) Platform {
	p := Platform{
		GOOS:   goos,
		GOARCH: goarch,
		Errors: map[string]string{},
	}
	if out, err := probeCommand("go", "version"); err != nil {
		p.Errors["go"] = err.Error()
		p.GOVersion = runtime.Version()
	} else {
		p.GOVersion = out
	}
	p.NodeVersion = probeVersion("node", "node", &p)
	p.PNPMVersion = probeVersion("pnpm", "pnpm", &p)
	p.GitVersion = probeVersion("git", "git", &p)
	if len(p.Errors) == 0 {
		p.Errors = nil
	}
	return p
}

// probeVersion records one tool's --version output, or null plus the reason.
func probeVersion(field, command string, p *Platform) *string {
	out, err := probeCommand(command, "--version")
	if err != nil {
		p.Errors[field] = err.Error()
		return nil
	}
	return ptr(out)
}

// probeCommand runs a short version query and returns trimmed combined output.
func probeCommand(name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("找不到 %s（PATH 中不可执行）", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg != "" {
			return "", fmt.Errorf("%s 执行失败（%s）：%s", name, path, clipText(msg, 300))
		}
		return "", fmt.Errorf("%s 执行失败（%s）：%w", name, path, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// clipText bounds a diagnostic string that lands in the manifest.
func clipText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
