package host

import "testing"

// The unit suffix is asserted, not assumed: a status file whose unit changed
// would otherwise be multiplied by 1024 into a wrong number that still looks
// plausible.
func TestParseVmHWMTakesTheKernelUnit(t *testing.T) {
	status := []byte("Name:\tpython3\nVmPeak:\t 2000000 kB\nVmHWM:\t  1048576 kB\nVmRSS:\t  1000000 kB\n")
	got, ok := parseVmHWM(status)
	if !ok {
		t.Fatal("VmHWM line was not recognised")
	}
	if want := uint64(1048576) * 1024; got != want {
		t.Fatalf("VmHWM = %d bytes, want %d", got, want)
	}
}

// A missing field is "unavailable", never a zero reading: 0 bytes reads as "it
// stayed tiny", which is the opposite of what a missing peak means.
func TestParseVmHWMMissingFieldIsUnavailable(t *testing.T) {
	for name, status := range map[string]string{
		"no VmHWM":     "Name:\tpython3\nVmRSS:\t  1000 kB\n",
		"no unit":      "VmHWM:\t 1048576\n",
		"wrong unit":   "VmHWM:\t 1048576 MB\n",
		"not a number": "VmHWM:\t many kB\n",
		"truncated":    "VmHWM:\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseVmHWM([]byte(status))
			if ok {
				t.Fatalf("parsed %d bytes from %q; must be unavailable", got, status)
			}
			if got != 0 {
				t.Fatalf("unavailable reading returned %d, want 0 only as a placeholder", got)
			}
		})
	}
}

// /proc/<pid>/stat puts a free-form comm in parentheses, and a command name may
// contain spaces or ')'. Fields after it are therefore located from the last
// ')', not by index from the start.
func TestParsePgrpSurvivesCommasAndParentheses(t *testing.T) {
	stat := []byte("4242 (python3 (poc) run) S 4241 4199 4199 0 -1 4194560")
	got, ok := parsePgrp(stat)
	if !ok || got != 4199 {
		t.Fatalf("parsePgrp = %d, %v; want 4199, true", got, ok)
	}
}

func TestParsePgrpRejectsShortOrMalformedStat(t *testing.T) {
	for name, stat := range map[string]string{
		"no comm":      "4242 S 4241",
		"too short":    "4242 (x) S 4241",
		"zero pgrp":    "4242 (x) S 4241 0",
		"not a number": "4242 (x) S 4241 x",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := parsePgrp([]byte(stat)); ok {
				t.Fatalf("parsePgrp(%q) = %d; must be unavailable", stat, got)
			}
		})
	}
}

// A host that is not supervising anything answers nil, so the API records
// nothing; a host that exists but has no live child answers one unavailable
// entry, so the run's journal still says the sample was attempted.
func TestChildPeakRSSDistinguishesNoHostFromNoChild(t *testing.T) {
	var nilHost *Host
	if got := nilHost.ChildPeakRSS(); got != nil {
		t.Fatalf("nil host = %v, want nil", got)
	}
	empty := &Host{}
	got := empty.ChildPeakRSS()
	if len(got) != 1 || got[0].Available || got[0].Reason == "" {
		t.Fatalf("idle host = %+v, want one unavailable entry with a reason", got)
	}
}
