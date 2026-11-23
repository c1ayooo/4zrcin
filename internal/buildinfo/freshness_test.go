package buildinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate's embedded-artifact check end to end, on a throwaway git repository:
// a rebuild that matches the committed product passes, a stale commit fails, and
// a dirty-but-untouched working tree does not.
func TestCheckEmbeddedFreshness(t *testing.T) {
	dir := initRepo(t)
	writeStatic := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(EmbeddedDir), filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeStatic("index.html", "<html>v1</html>")
	writeStatic("assets/app.js", "app-v1\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "build static")

	// A rebuild that produces identical bytes is not a freshness failure.
	artifacts, err := CheckEmbeddedFreshness(dir, func() error {
		writeStatic("index.html", "<html>v1</html>")
		writeStatic("assets/app.js", "app-v1\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Changed) != 0 {
		t.Fatalf("unchanged rebuild reported changes: %v", artifacts.Changed)
	}
	if artifacts.BeforeSHA256 != artifacts.AfterSHA256 {
		t.Fatalf("digests differ: %s vs %s", artifacts.BeforeSHA256, artifacts.AfterSHA256)
	}
	if artifacts.Dir != EmbeddedDir {
		t.Fatalf("dir = %q", artifacts.Dir)
	}

	// A rebuild that produces a difference means the commit was stale.
	artifacts, err = CheckEmbeddedFreshness(dir, func() error {
		writeStatic("assets/app.js", "app-v2\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(artifacts.Changed, ",") != "assets/app.js" {
		t.Fatalf("changed = %v", artifacts.Changed)
	}
	if artifacts.BeforeSHA256 == artifacts.AfterSHA256 {
		t.Fatal("aggregate digest did not change")
	}

	// The tree now has an uncommitted rebuild (the same situation as a dirty
	// worktree before the gate): a second rebuild with the same bytes must not
	// report a new difference.
	artifacts, err = CheckEmbeddedFreshness(dir, func() error {
		writeStatic("assets/app.js", "app-v2\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Changed) != 0 {
		t.Fatalf("pre-existing dirty tree reported as failure: %v", artifacts.Changed)
	}
	if len(artifacts.BeforeStatus) == 0 || len(artifacts.AfterStatus) == 0 {
		t.Fatalf("dirty tree status not recorded: %v %v", artifacts.BeforeStatus, artifacts.AfterStatus)
	}

	// A failing build is reported as the build error, not as a freshness result.
	if _, err := CheckEmbeddedFreshness(dir, func() error { return errors.New("pnpm failed") }); err == nil || !strings.Contains(err.Error(), "pnpm failed") {
		t.Fatalf("build error = %v", err)
	}
}

// TestCheckEmbeddedFreshnessIgnoresLineEndings: on Windows a checkout restored
// by git holds CRLF while `vite build` writes LF. That is the same product in two
// byte streams; reporting it as "embedded artifacts are stale" made the gate fail
// on a clean tree (2026-11-23, 87 assets). A real content change must still fail,
// and binary content stays byte-exact.
func TestCheckEmbeddedFreshnessIgnoresLineEndings(t *testing.T) {
	dir := t.TempDir()
	writeText := func(body string) error {
		path := filepath.Join(dir, filepath.FromSlash(EmbeddedDir), "assets", "app.js")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(body), 0o644)
	}
	writeBin := func(body []byte) error {
		path := filepath.Join(dir, filepath.FromSlash(EmbeddedDir), "assets", "img.bin")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, body, 0o644)
	}
	if err := writeText("a\r\nb\r\nc\r\n"); err != nil {
		t.Fatal(err)
	}

	artifacts, err := CheckEmbeddedFreshness(dir, func() error { return writeText("a\nb\nc\n") })
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Changed) != 0 {
		t.Fatalf("CRLF checkout vs LF rebuild reported as stale: %v", artifacts.Changed)
	}
	if artifacts.BeforeSHA256 != artifacts.AfterSHA256 {
		t.Fatalf("aggregate digests differ across line endings: %s vs %s", artifacts.BeforeSHA256, artifacts.AfterSHA256)
	}

	// A rebuild whose content really differs is still a failure.
	artifacts, err = CheckEmbeddedFreshness(dir, func() error { return writeText("a\nb\nc\nd\n") })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(artifacts.Changed, ",") != "assets/app.js" {
		t.Fatalf("real change not reported: %v", artifacts.Changed)
	}

	// Binary content is never normalised: a NUL byte in the head keeps it exact.
	if err := writeBin([]byte("x\x00y\r\nz")); err != nil {
		t.Fatal(err)
	}
	artifacts, err = CheckEmbeddedFreshness(dir, func() error { return writeBin([]byte("x\x00y\nz")) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(artifacts.Changed, ",") != "assets/img.bin" {
		t.Fatalf("binary CRLF change not reported: %v", artifacts.Changed)
	}
}

// TestCheckEmbeddedFreshnessMissingDir: a checkout without the embedded product
// is snapshotted as empty (Exists=false) and a build that creates it shows every
// file as changed.
func TestCheckEmbeddedFreshnessMissingDir(t *testing.T) {
	dir := t.TempDir()
	artifacts, err := CheckEmbeddedFreshness(dir, func() error {
		target := filepath.Join(dir, filepath.FromSlash(EmbeddedDir))
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, "index.html"), []byte("new"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(artifacts.Changed, ",") != "index.html" {
		t.Fatalf("changed = %v", artifacts.Changed)
	}
}
