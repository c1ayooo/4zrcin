//go:build windows

package noderes

import (
	"time"

	"golang.org/x/sys/windows"
)

// Sampler on Windows reports the data volume's free space only: CPU and
// memory need performance counters this build does not read, and the console
// renders the missing counters as 不支持 instead of zeros.
type Sampler struct {
	dataDir string
	scanPID int
}

func NewSampler(dataDir string, scanPID int) *Sampler {
	return &Sampler{dataDir: dataDir, scanPID: scanPID}
}

func (s *Sampler) Sample(window time.Duration) Snapshot {
	snap := Snapshot{CollectedAt: time.Now().UTC()}
	pathPtr, err := windows.UTF16PtrFromString(s.dataDir)
	if err == nil {
		var free, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err == nil {
			// free is the caller's available bytes; total the volume size.
			snap.DiskFree = free
			snap.DiskTotal = total
		}
	}
	return snap
}
