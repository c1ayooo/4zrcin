//go:build !windows

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// procRoot is the procfs mount. It is a variable rather than a constant so the
// unavailable path ("this entry is gone") is testable in a temporary directory:
// racing a real process exit would make the test flaky and prove nothing.
var procRoot = "/proc"

func childPeakRSS(roots []int) []PeakRSS { return peakRSSInGroups(procRoot, roots) }

// peakRSSInGroups reports every process in the process groups of roots. Each
// supervised child is started with Setpgid and its PTY/tool children inherit
// that group, so the group is this instance's own subtree — matched by group
// membership, not by executable name, which would catch another instance's
// harness on a machine that runs several.
func peakRSSInGroups(root string, roots []int) []PeakRSS {
	groups := map[int]bool{}
	var out []PeakRSS
	for _, pid := range roots {
		pgrp, ok := readPgrp(root, pid)
		if !ok {
			out = append(out, PeakRSS{PID: pid,
				Reason: fmt.Sprintf("读不到进程 %d 的进程组（已退出或 stat 不可读）", pid)})
			continue
		}
		groups[pgrp] = true
	}
	if len(groups) == 0 {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return append(out, PeakRSS{Reason: "读 " + root + "：" + err.Error()})
	}
	var pids []int
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil || pid <= 0 {
			continue
		}
		if pgrp, ok := readPgrp(root, pid); ok && groups[pgrp] {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	for _, pid := range pids {
		out = append(out, procPeakRSS(root, pid))
	}
	return out
}

func readPgrp(root string, pid int) (int, bool) {
	data, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, false
	}
	return parsePgrp(data)
}

// procPeakRSS reads one /proc entry. A process that exited between the group
// scan and this read is reported unavailable with the reason instead of being
// dropped: the caller must be able to see that a sample existed and was missed.
func procPeakRSS(root string, pid int) PeakRSS {
	out := PeakRSS{PID: pid}
	dir := filepath.Join(root, strconv.Itoa(pid))
	if comm, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
		out.Name = strings.TrimSpace(string(comm))
	}
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		out.Reason = "读 status 失败（进程已退出）：" + err.Error()
		return out
	}
	bytes, ok := parseVmHWM(data)
	if !ok {
		out.Reason = "status 里没有可解析的 VmHWM（该内核不提供峰值字段）"
		return out
	}
	out.Bytes, out.Available = bytes, true
	return out
}
