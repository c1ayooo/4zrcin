package noderes

import (
	"os"
	"path/filepath"
	"testing"
)

// The parsers are the contract the cards render: one malformed line must not
// zero the whole counter, and KiB fields arrive as bytes.
func TestReadMemInfoParsesAndToleratesJunk(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "meminfo")
	raw := "MemTotal:       16384000 kB\n" +
		"junk line without colon\n" +
		"MemAvailable:    8192000 kB\n" +
		"MemFree:         1000 kB\n"
	if err := os.WriteFile(fake, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := readMemInfoAt(fake)
	if err != nil {
		t.Fatal(err)
	}
	if mi.total != 16384000*1024 {
		t.Fatalf("total = %d", mi.total)
	}
	if mi.available != 8192000*1024 {
		t.Fatalf("available = %d", mi.available)
	}
	if _, err := readMemInfoAt(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing meminfo must error")
	}
}

func TestReadVMRSSHonoursKilobytes(t *testing.T) {
	// A real /proc read can only be exercised for a live pid; the parse is
	// covered through readVMRSSFile.
	dir := t.TempDir()
	fake := filepath.Join(dir, "status")
	if err := os.WriteFile(fake, []byte("Name:\tscan\nVmRSS:\t  123456 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readVMRSSFile(fake); got != 123456*1024 {
		t.Fatalf("rss = %d", got)
	}
}

// The disk figure describes the volume of the given directory, and an
// unprivileged caller sees Bavail (its own free), not root's slack.
func TestStatfsVolumeReportsTheGivenDir(t *testing.T) {
	dir := t.TempDir()
	df, err := statfsVolume(dir)
	if err != nil {
		t.Fatal(err)
	}
	if df.total == 0 || df.free == 0 {
		t.Fatalf("statfs = %+v", df)
	}
	if _, err := statfsVolume(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("statfs of a missing dir must error")
	}
}
