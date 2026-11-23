//go:build !windows

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeProc lays out one /proc/<pid> directory: stat, status and comm.
func writeFakeProc(t *testing.T, root string, pid, pgrp int, hwmKB int64, comm string) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// /proc/<pid>/stat after comm: state, ppid, pgrp, session. The pgrp column
	// must actually carry pgrp; this fixture used to write pid there, which made
	// the "tool child" look like its own group leader. The file only builds on
	// !windows, so the mistake never showed until the suite ran on Linux.
	stat := fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 0\n", pid, comm, pgrp, pgrp)
	status := fmt.Sprintf("Name:\t%s\nVmPeak:\t %d kB\nVmHWM:\t %d kB\n", comm, hwmKB, hwmKB)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The scan node and the interpreter a tool call spawned share a process group;
// another instance's harness does not. Matching by group is what keeps the
// sample to this instance's own subtree without matching executable names.
func TestPeakRSSInGroupsReadsOnlyThisInstancesSubtree(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 100, 100, 2048, "cyber-scan")
	writeFakeProc(t, root, 101, 100, 3145728, "python3")
	writeFakeProc(t, root, 200, 200, 1024, "cyber-harness")

	peaks := peakRSSInGroups(root, []int{100})
	got := map[int]PeakRSS{}
	for _, p := range peaks {
		got[p.PID] = p
	}
	if p, ok := got[100]; !ok || !p.Available || p.Bytes != 2048*1024 {
		t.Fatalf("scan node = %+v, want 2048 KiB available", p)
	}
	if p, ok := got[101]; !ok || !p.Available || p.Bytes != 3145728*1024 {
		t.Fatalf("tool child = %+v, want 3 GiB available", p)
	}
	if p, ok := got[101]; ok && p.Name != "python3" {
		t.Fatalf("tool child name = %q", p.Name)
	}
	if _, ok := got[200]; ok {
		t.Fatalf("a process from another process group was sampled: %+v", got[200])
	}
}

// A group member whose status vanished between the group scan and the read is
// reported unavailable, not dropped: the record must show that a sample was
// attempted and missed.
func TestPeakRSSInGroupsReportsMembersWithMissingStatus(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 100, 100, 1024, "cyber-scan")
	dir := filepath.Join(root, "102")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "102 (python3) S 1 100 100 0 -1 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}

	peaks := peakRSSInGroups(root, []int{100})
	for _, p := range peaks {
		if p.PID != 102 {
			continue
		}
		if p.Available || p.Bytes != 0 || p.Reason == "" {
			t.Fatalf("member without status = %+v, want unavailable with a reason", p)
		}
		return
	}
	t.Fatalf("member 102 is missing from %+v", peaks)
}

// A procfs that cannot be read at all is unavailable with the reason; it must
// never be summarised as "no child process was large".
func TestPeakRSSInGroupsReportsUnreadableProcRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-procfs")
	peaks := peakRSSInGroups(missing, []int{321})
	if len(peaks) == 0 {
		t.Fatal("unreadable procfs produced no record at all")
	}
	unreadableRoot := false
	for _, p := range peaks {
		if p.Available || p.Reason == "" {
			t.Fatalf("entry from an unreadable procfs = %+v, want unavailable with a reason", p)
		}
		if p.PID == 0 {
			unreadableRoot = true
		}
	}
	if !unreadableRoot {
		t.Fatalf("the unreadable procfs itself was not reported: %+v", peaks)
	}
}

// End to end on this platform: a real child of this test, in its own process
// group, is sampled through /proc with a non-zero peak.
func TestPeakRSSReadsALiveChildOnLinux(t *testing.T) {
	cmd := sleeperCommand()
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	peaks := childPeakRSS([]int{cmd.Process.Pid})
	for _, p := range peaks {
		if p.PID != cmd.Process.Pid {
			continue
		}
		if !p.Available || p.Bytes == 0 {
			t.Fatalf("live child = %+v, want an available non-zero VmHWM", p)
		}
		return
	}
	t.Fatalf("live child %d missing from %+v", cmd.Process.Pid, peaks)
}
