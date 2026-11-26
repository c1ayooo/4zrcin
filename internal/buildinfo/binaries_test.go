package buildinfo

import "testing"

// The capture recipe's size comparison (docs/04 §3.3) reads the recorded
// facts only and stays nil unless both scan artifacts exist.
func TestCaptureSavedComparesScanRecipes(t *testing.T) {
	if got := captureSaved([]Binary{{Name: "cyber-scan", Size: 100}}); got != nil {
		t.Fatalf("capture missing: got %d", *got)
	}
	got := captureSaved([]Binary{
		{Name: "cyber-harness", Size: 500},
		{Name: "cyber-scan", Size: 189186646},
		{Name: "cyber-scan-capture", Size: 161826140},
	})
	if got == nil || *got != 27360506 {
		t.Fatalf("saved = %v", got)
	}
	if got := captureSaved([]Binary{{Name: "cyber-scan", Size: 100}, {Name: "cyber-scan-capture", Missing: true}}); got != nil {
		t.Fatalf("missing capture must stay nil, got %d", *got)
	}
}
