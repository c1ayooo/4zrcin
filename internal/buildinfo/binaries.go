package buildinfo

// Binary identity and manifest output. The digests here are what let a later
// reader tie a test result to the exact executables that were exercised — the
// acceptance criterion P0-1 demands ("从清单可追溯到本次测试的源码与二进制").

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// collectHarness records the harness checkout and the Hub/scan binaries built
// from it. A skipped or missing harness source is a recorded fact; the gate
// decides separately whether it is allowed to be missing.
func collectHarness(root, harnessDir, binDir, exe string) HarnessIdentity {
	h := HarnessIdentity{
		Binaries: []Binary{
			collectBinary(root, "cyber-harness", filepath.Join(binDir, "cyber-harness"+exe)),
			collectBinary(root, "cyber-scan", filepath.Join(binDir, "cyber-scan"+exe)),
		},
	}
	if strings.TrimSpace(harnessDir) == "" {
		h.SourceMissing = true
		h.SourceError = ptr("未指定 harness 源码目录（-skip-harness 或未找到 HARNESS_DIR / .build/harness-src）")
		return h
	}
	abs := absFrom(root, harnessDir)
	h.SourceDir = manifestPath(root, abs)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		h.SourceMissing = true
		h.SourceError = ptr("harness 源码目录不存在：" + manifestPath(root, abs))
		return h
	}
	commit, branch, subject, dirty, err := gitIdentity(abs)
	if err != nil {
		h.SourceError = ptr(err.Error())
		return h
	}
	h.Commit, h.Branch, h.Subject, h.Dirty = commit, branch, subject, dirty
	return h
}

// collectBinary hashes one executable and records its --version answer. A
// missing binary is not an error: a manifest-only run happens before builds.
func collectBinary(root, name, path string) Binary {
	b := Binary{Name: name, Path: manifestPath(root, path)}
	abs := absFrom(root, path)
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		b.Missing = true
		b.VersionError = ptr("文件不存在（尚未构建）")
		return b
	}
	size, sum, _, err := hashFile(abs)
	if err != nil {
		b.VersionError = ptr("摘要失败：" + err.Error())
		return b
	}
	b.Size = size
	b.SHA256 = sum

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, abs, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	// Collapse newlines: a multiline usage dump must stay one manifest line.
	output := collapseWhitespace(out.String())
	if runErr != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		b.VersionExitCode = &code
		// Not every tool has a --version flag. Record that fact instead of
		// stuffing a usage dump into the manifest.
		msg := fmt.Sprintf("--version 退出码 %d：%s", code, clipText(output, 300))
		b.VersionError = &msg
		return b
	}
	code := 0
	b.VersionExitCode = &code
	b.Version = clipText(output, 400)
	return b
}

// collapseWhitespace folds one logical line into a manifest-friendly string.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// writeManifest writes indented JSON atomically enough for a local artifact: a
// reader never sees half a file because the manifest is generated once, after
// the checks, and stage+rename keeps a concurrent reader safe.
func writeManifest(path string, m *Manifest) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("缺少清单输出路径")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".build-manifest-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, abs)
}
