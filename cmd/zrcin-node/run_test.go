package main

import "testing"

// The capture recipe is a launch flag since docs/19 P6: the launcher downloads
// the one cyber-scan binary for every enrollment and turns the recipe into
// --capture-only on the command line. A full recipe must not carry the flag.
func TestScanArgsCarryTheCaptureFlag(t *testing.T) {
	full := scanArgs(&Config{Name: "edge", NodeID: "n-1"})
	for _, arg := range full {
		if arg == "--capture-only" {
			t.Fatalf("full recipe launch line carries the capture flag: %v", full)
		}
	}

	capture := scanArgs(&Config{Name: "fence", NodeID: "n-2", Recipe: "capture"})
	found := false
	for _, arg := range capture {
		if arg == "--capture-only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("capture recipe launch line lacks --capture-only: %v", capture)
	}
}
