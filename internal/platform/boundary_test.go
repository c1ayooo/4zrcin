package platform

import (
	"strings"
	"testing"
)

// P0-6 boundary rule: process and network isolation are not claimed on either
// platform. This test asserts the Windows and Linux declarations are the same
// shape and the same "not enforced" answer; a future platform that really
// enforces one of them has to change this test deliberately.
func TestIsolationBoundariesAreNotClaimedOnEitherPlatform(t *testing.T) {
	want := map[string]bool{"process": false, "network": false, "filesystem": false, "credentials": false}
	for _, goos := range []string{"windows", "linux"} {
		boundaries := IsolationBoundaries(goos)
		if len(boundaries) != len(want) {
			t.Fatalf("%s: %d boundaries", goos, len(boundaries))
		}
		seen := map[string]bool{}
		for _, b := range boundaries {
			if _, ok := want[b.ID]; !ok {
				t.Fatalf("%s: unexpected boundary %q", goos, b.ID)
			}
			if b.Status != StatusUnsupportedBoundary || b.Enforced {
				t.Fatalf("%s: %s must be unsupported and not enforced: %+v", goos, b.ID, b)
			}
			if strings.TrimSpace(b.Detail) == "" || strings.TrimSpace(b.Capability) == "" {
				t.Fatalf("%s: %s lacks explanatory text", goos, b.ID)
			}
			seen[b.ID] = true
		}
		for id := range want {
			if !seen[id] {
				t.Fatalf("%s: missing boundary %q", goos, id)
			}
		}
	}
	// The declaration must not differ merely because the host is Windows.
	windowsBoundary := IsolationBoundaries("windows")
	linuxBoundary := IsolationBoundaries("linux")
	if len(windowsBoundary) != len(linuxBoundary) {
		t.Fatalf("windows/linux boundary count differs: %d vs %d", len(windowsBoundary), len(linuxBoundary))
	}
	for i := range windowsBoundary {
		if windowsBoundary[i].ID != linuxBoundary[i].ID ||
			windowsBoundary[i].Status != linuxBoundary[i].Status ||
			windowsBoundary[i].Enforced != linuxBoundary[i].Enforced {
			t.Fatalf("boundary %d differs by platform: %+v vs %+v", i, windowsBoundary[i], linuxBoundary[i])
		}
	}
}

func TestBoundaryReportNamesBothPlatformsAsEquallySupported(t *testing.T) {
	for goos, family := range map[string]string{"windows": "Windows", "linux": "Linux"} {
		report := Report(goos, "amd64")
		if report.Schema != BoundarySchema {
			t.Fatalf("%s: schema = %q", goos, report.Schema)
		}
		if report.Platform.GOOS != goos || report.Platform.GOARCH != "amd64" || report.Platform.Family != Describe(goos, "").Family {
			t.Fatalf("%s: platform descriptor = %+v", goos, report.Platform)
		}
		joined := strings.Join(report.Limitations, "\n")
		for _, needle := range []string{family, "同等支持", "均未实现", StatusUnsupportedBoundary} {
			if !strings.Contains(joined, needle) {
				t.Fatalf("%s: limitations miss %q:\n%s", goos, needle, joined)
			}
		}
	}
}

// The boundary document is status data, not a policy grant: it must be plain
// values with no writer/authority field that a caller could misread.
func TestBoundaryReportIsReadOnlyStatus(t *testing.T) {
	report := Report("linux", "arm64")
	if len(report.Isolation) == 0 || len(report.Limitations) == 0 {
		t.Fatalf("empty report: %+v", report)
	}
	for _, b := range report.Isolation {
		if b.Enforced {
			t.Fatalf("report claims enforcement for %s", b.ID)
		}
	}
}
