package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunInputPublishesOneCompleteSnapshot(t *testing.T) {
	_, st := newFocusTestServer(t)
	in, _ := focusIntentFixture(t, st)
	work := t.TempDir()
	snapshot := st.Snapshot()
	read := func() runInputManifest {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(runInputFile)))
		if err != nil {
			t.Fatal(err)
		}
		var m runInputManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if err := publishRunInput(work, "r_first", snapshot, "", in.ID); err != nil {
		t.Fatal(err)
	}
	first := read()
	if first.State != "ready" || first.RunID != "r_first" || first.Snapshot == nil || *first.Snapshot != st.Watermark() {
		t.Fatalf("first manifest = %+v", first)
	}
	for _, rel := range []string{first.Graph, first.Focus, first.Context} {
		if info, err := os.Stat(filepath.Join(work, filepath.FromSlash(rel))); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("published file %s: %v", rel, err)
		}
	}
	// Focus generation fails for a missing direction, after the graph writer.
	if err := publishRunInput(work, "r_next", snapshot, "", "missing"); err == nil {
		t.Fatal("published an incomplete generation")
	}
	next := read()
	if next.RunID != "r_next" || next.State != "unavailable" || next.Graph != "" || next.Focus != "" || next.Context != "" {
		t.Fatalf("failed generation still advertises paths: %+v", next)
	}
}
