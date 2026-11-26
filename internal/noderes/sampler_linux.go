//go:build linux

package noderes

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Sampler keeps the previous CPU tick so a sample can report the busy
// fraction over the window it measures, and knows which volume the data
// directory lives on.
type Sampler struct {
	dataDir string
	scanPID int
	prev    cpuTimes
	hasPrev bool
}

type cpuTimes struct {
	busy  uint64
	total uint64
}

// NewSampler watches one scan process (0 = none) and the volume holding
// dataDir.
func NewSampler(dataDir string, scanPID int) *Sampler {
	return &Sampler{dataDir: dataDir, scanPID: scanPID}
}

// Sample reads the host counters once. The CPU percentage is measured across
// a short window (about 600 ms), so a caller polling at the heartbeat cadence
// gets a real utilisation figure rather than a division by zero.
func (s *Sampler) Sample(window time.Duration) Snapshot {
	snap := Snapshot{CollectedAt: time.Now().UTC()}

	if cur, err := readCPUTimes(); err == nil {
		if s.hasPrev {
			snap.CPUPercent = cpuPercent(s.prev, cur)
		}
		s.prev, s.hasPrev = cur, true
		if window > 0 {
			time.Sleep(window)
			if cur, err := readCPUTimes(); err == nil {
				snap.CPUPercent = cpuPercent(s.prev, cur)
				s.prev = cur
			}
		}
	}

	if mi, err := readMemInfoAt("/proc/meminfo"); err == nil {
		snap.MemTotal = mi.total
		snap.MemUsed = mi.total - mi.available
	}
	if df, err := statfsVolume(s.dataDir); err == nil {
		snap.DiskFree = df.free
		snap.DiskTotal = df.total
	}
	if s.scanPID > 0 {
		snap.ScanRSS = readVMRSSFile(fmt.Sprintf("/proc/%d/status", s.scanPID))
	}
	return snap
}

func cpuPercent(prev, cur cpuTimes) float64 {
	dTotal := cur.total - prev.total
	if dTotal <= 0 {
		return 0
	}
	dBusy := cur.busy - prev.busy
	if dBusy < 0 {
		return 0
	}
	return float64(dBusy) / float64(dTotal) * 100
}

// readCPUTimes sums /proc/stat's first line into busy/idle buckets.
func readCPUTimes() (cpuTimes, error) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	if !strings.HasPrefix(line, "cpu ") {
		return cpuTimes{}, fmt.Errorf("/proc/stat: no cpu line")
	}
	fields := strings.Fields(strings.TrimPrefix(line, "cpu "))
	if len(fields) < 4 {
		return cpuTimes{}, fmt.Errorf("/proc/stat: short cpu line")
	}
	var nums []uint64
	for _, f := range fields {
		v, perr := strconv.ParseUint(f, 10, 64)
		if perr != nil {
			return cpuTimes{}, fmt.Errorf("/proc/stat: %w", perr)
		}
		nums = append(nums, v)
	}
	// user nice system idle(+iowait) …; iowait is waiting, not working, but it
	// is not idle either — count it as total-only so a busy-IO host does not
	// read as idle.
	idle := nums[3]
	if len(nums) > 4 {
		idle += nums[4]
	}
	total := uint64(0)
	for _, v := range nums {
		total += v
	}
	return cpuTimes{busy: total - idle, total: total}, nil
}

type memInfo struct {
	total     uint64
	available uint64
}

func readMemInfoAt(path string) (memInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return memInfo{}, err
	}
	var mi memInfo
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := cutMemField(line)
		if !ok {
			continue
		}
		switch key {
		case "MemTotal":
			mi.total = value
		case "MemAvailable":
			mi.available = value
		}
	}
	if mi.total == 0 {
		return memInfo{}, fmt.Errorf("/proc/meminfo: no MemTotal")
	}
	return mi, nil
}

// cutMemField parses one "Key:  123 kB" line into a byte count.
func cutMemField(line string) (string, uint64, bool) {
	key, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", 0, false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", 0, false
	}
	v, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", 0, false
	}
	// meminfo reports KiB; report bytes like everything else here.
	return strings.TrimSpace(key), v * 1024, true
}

// statfsVolume measures the filesystem holding dir — for the launcher that is
// its data directory, for the control plane its own <data>.
func statfsVolume(dir string) (struct{ free, total uint64 }, error) {
	var out struct{ free, total uint64 }
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return out, err
	}
	out.total = uint64(st.Blocks) * uint64(st.Bsize)
	out.free = uint64(st.Bavail) * uint64(st.Bsize) // unprivileged free, not root's slack
	return out, nil
}

// readVMRSSFile reads VmRSS (current resident set) out of one status file.
func readVMRSSFile(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
		if len(fields) == 0 {
			return 0
		}
		v, perr := strconv.ParseUint(fields[0], 10, 64)
		if perr != nil {
			return 0
		}
		return v * 1024 // kB → bytes
	}
	return 0
}
