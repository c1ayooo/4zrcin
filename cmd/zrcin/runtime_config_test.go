package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeHarnessArgsExplicitlyUseFilteredConfiguration(t *testing.T) {
	h, source := singleModelHub(t)
	m := h.settings.Effective()
	m.ContextWindow, m.MaxTokens = 32000, 8192
	args, nodeConfigPath, err := runtimeHarnessArgs(h.settings, t.TempDir(), m)
	if err != nil {
		t.Fatal(err)
	}
	// Both documents are content-addressed: with no product sections their
	// contents coincide and sharing one file is harmless (both registries
	// accept it). The split only matters once a product section exists —
	// asserted in internal/settings' two-document tests.
	if nodeConfigPath == "" {
		t.Fatalf("node document missing: args=%v", args)
	}
	if len(args) != 6 || args[0] != "--config" || !filepath.IsAbs(args[1]) || args[1] == source || strings.Join(args[2:], " ") != "--context-window 32000 --max-tokens 8192" {
		t.Fatalf("runtime arguments=%v", args)
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "paused:") {
		t.Fatal("boot/restart configuration still contains admission metadata")
	}
}

// This exercises the actual strict parser without starting a service or making
// a model request. The filtered runtime document must validate with both the
// rc10 Hub and the scan product.
func TestRuntimeHarnessConfigStrictParser(t *testing.T) {
	bin := strings.TrimSpace(os.Getenv("ZRCIN_HARNESS_BIN"))
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("ZRCIN_AISCAN_BIN"))
	}
	if bin == "" {
		t.Skip("set ZRCIN_HARNESS_BIN to validate with the real harness parser")
	}
	h, _ := singleModelHub(t)
	dir := t.TempDir()
	validate := func(binary string, args []string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append(args, "config", "validate")...)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	scan := os.Getenv("ZRCIN_SCAN_BIN")
	if scan == "" {
		scan = filepath.Join(filepath.Dir(bin), "cyber-scan"+filepath.Ext(bin))
	}
	if st, err := os.Stat(scan); err != nil || st.IsDir() {
		t.Skip("set ZRCIN_SCAN_BIN (or put cyber-scan next to ZRCIN_HARNESS_BIN) for the strict parser check")
	}
	// rc10 preserves foreign metadata instead of rejecting the source. The
	// materialized file must still remove admission metadata and validate on
	// both products; the unit test above checks the actual filtered bytes.
	hubArgs, nodeConfigPath, err := runtimeHarnessArgs(h.settings, dir, h.settings.Effective())
	if err != nil {
		t.Fatal(err)
	}
	// Each product validates its own document: the Hub's registry rejects
	// scan-side product sections, the scan's registry requires them top-level.
	if out, err := validate(bin, hubArgs); err != nil {
		t.Fatalf("hub rejected its runtime configuration: err=%v output=%s", err, out)
	}
	if out, err := validate(scan, append([]string{"--config", nodeConfigPath}, hubArgs[2:]...)); err != nil {
		t.Fatalf("scan rejected its runtime configuration: err=%v output=%s", err, out)
	}
}
