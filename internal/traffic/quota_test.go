package traffic

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	native "github.com/kksty/tianwu/internal/aop/traffic"
)

// dirBytes is the real on-disk size of the capture directory. Tests compare it
// with the quota cache instead of trusting the cache's own arithmetic.
func dirBytes(t *testing.T, cases string) int64 {
	t.Helper()
	var total int64
	root := filepath.Join(cases, ".http")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func quotaFlow(id string, bodySize int) *native.Flow {
	return &native.Flow{
		Id: id, Complete: true,
		Request: &native.HttpRequest{
			Method: "POST", Url: "https://example.test/" + id, Protocol: "https",
			Headers: []*native.Header{{Name: "X-Request", Value: "yes"}},
			Body:    bytes.Repeat([]byte("r"), bodySize),
		},
		Response: &native.HttpResponse{
			StatusCode: 200,
			Headers:    []*native.Header{{Name: "Content-Type", Value: "application/json"}},
			Body:       bytes.Repeat([]byte("s"), bodySize),
		},
	}
}

// The first Used call walks the existing directory (a project predates the
// budget), then later saves only add what this process wrote.
func TestProjectHTTPQuotaWalksOnceThenTracksWrites(t *testing.T) {
	cases := t.TempDir()
	httpDir := filepath.Join(cases, ".http")
	if err := os.MkdirAll(httpDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(httpDir, "a.pb"), bytes.Repeat([]byte("a"), 300), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(httpDir, "a.json"), bytes.Repeat([]byte("j"), 400), 0600); err != nil {
		t.Fatal(err)
	}
	quota := NewProjectHTTPQuota(cases, 1<<20)
	used, err := quota.Used()
	if err != nil || used != 700 {
		t.Fatalf("first walk = %d, %v; want 700", used, err)
	}
	if _, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", quotaFlow("flow-1", 10), nil, quota); err != nil {
		t.Fatal(err)
	}
	used, err = quota.Used()
	if err != nil {
		t.Fatal(err)
	}
	if want := dirBytes(t, cases); used != want {
		t.Fatalf("cached usage = %d, disk = %d", used, want)
	}
	if used <= 700 {
		t.Fatalf("saved capture did not increase usage: %d", used)
	}
	if quota.Max() != 1<<20 || quota.Unlimited() {
		t.Fatalf("max/unlimited misreported: %d/%v", quota.Max(), quota.Unlimited())
	}
}

func TestProjectHTTPQuotaMissingDirectoryIsEmpty(t *testing.T) {
	quota := NewProjectHTTPQuota(t.TempDir(), 1024)
	used, err := quota.Used()
	if err != nil || used != 0 {
		t.Fatalf("missing dir = %d, %v; want 0", used, err)
	}
	if NewProjectHTTPQuota(t.TempDir(), 0).Unlimited() != true {
		t.Fatal("max 0 must mean unlimited")
	}
	if (*ProjectHTTPQuota)(nil).Unlimited() != true {
		t.Fatal("nil quota must mean unlimited")
	}
}

// Concurrent quota-aware saves must leave the cached usage exactly on the disk
// total: every write is accounted once, under the tracker's lock.
func TestProjectHTTPQuotaConcurrentSavesAgreeWithDisk(t *testing.T) {
	cases := t.TempDir()
	quota := NewProjectHTTPQuota(cases, 1<<30)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := "flow-" + string(rune('a'+i))
			if _, err := SaveCaptureWithQuota(cases, "run-"+id, "intent-1", "session-1", "event-"+id, quotaFlow(id, 50), nil, quota); err != nil {
				t.Errorf("save %s: %v", id, err)
			}
		}()
	}
	wg.Wait()
	used, err := quota.Used()
	if err != nil {
		t.Fatal(err)
	}
	if want := dirBytes(t, cases); used != want {
		t.Fatalf("cached usage = %d, disk = %d", used, want)
	}
}

// The 1 KiB case: the existing project bytes plus the new flow exceed the
// budget, so this one observation keeps only index+headers/metadata. The body
// fields stay empty, the observed sizes stay as metadata, and the reason is
// explicit on both the index and the stored flow.
func TestCaptureQuotaStripsBodyWhenOverBudget(t *testing.T) {
	cases := t.TempDir()
	httpDir := filepath.Join(cases, ".http")
	if err := os.MkdirAll(httpDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := bytes.Repeat([]byte("seed"), 100) // 400 bytes
	if err := os.WriteFile(filepath.Join(httpDir, "seed.pb"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	quota := NewProjectHTTPQuota(cases, 1024)
	capture, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", quotaFlow("flow-quota", 400), nil, quota)
	if err != nil {
		t.Fatal(err)
	}
	if !capture.QuotaExceeded || !capture.PreviewOnly || !capture.Truncated {
		t.Fatalf("flags = quota %v preview %v truncated %v, want all true",
			capture.QuotaExceeded, capture.PreviewOnly, capture.Truncated)
	}
	if capture.Complete {
		t.Fatal("quota-stripped capture still claims to be complete")
	}
	if !strings.Contains(capture.Error, "项目 HTTP 正文配额已满") {
		t.Fatalf("error does not name the quota: %q", capture.Error)
	}
	if capture.RequestBytes != 400 || capture.ResponseBytes != 400 {
		t.Fatalf("observed sizes lost: %d/%d, want 400/400", capture.RequestBytes, capture.ResponseBytes)
	}

	read, stored, err := ReadCapture(cases, capture.Ref)
	if err != nil {
		t.Fatalf("quota capture is unreadable: %v", err)
	}
	if !read.QuotaExceeded || !read.PreviewOnly || !read.Truncated {
		t.Fatalf("read flags = %+v", read)
	}
	if len(stored.GetRequest().GetBody()) != 0 || len(stored.GetResponse().GetBody()) != 0 {
		t.Fatalf("stored bodies not empty: %d/%d", len(stored.GetRequest().GetBody()), len(stored.GetResponse().GetBody()))
	}
	if stored.GetRequest().GetMethod() != "POST" || stored.GetRequest().GetUrl() != "https://example.test/flow-quota" || stored.GetResponse().GetStatusCode() != 200 {
		t.Fatalf("metadata lost: %v", stored)
	}
	if len(stored.GetRequest().GetHeaders()) != 1 || stored.GetRequest().GetHeaders()[0].GetValue() != "yes" {
		t.Fatalf("request headers lost: %v", stored.GetRequest().GetHeaders())
	}
	if len(stored.GetResponse().GetHeaders()) != 1 || stored.GetResponse().GetHeaders()[0].GetValue() != "application/json" {
		t.Fatalf("response headers lost: %v", stored.GetResponse().GetHeaders())
	}

	rawIndex, err := os.ReadFile(captureBase(cases, capture.Ref) + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(rawIndex, &document); err != nil {
		t.Fatal(err)
	}
	if document["quota_exceeded"] != true || document["preview_only"] != true {
		t.Fatalf("index JSON flags = %v", document)
	}
	if got, _ := document["request_bytes"].(float64); int(got) != 400 {
		t.Fatalf("index request_bytes = %v, want 400", document["request_bytes"])
	}
	quotaIndexed := strings.Contains(string(rawIndex), `"quota_exceeded":true`)
	if !quotaIndexed {
		t.Fatalf("index JSON has no explicit quota marker: %s", rawIndex)
	}

	// Deliberately not part of the budget accounting: the pre-existing file.
	after, err := os.ReadFile(filepath.Join(httpDir, "seed.pb"))
	if err != nil || !bytes.Equal(after, seed) {
		t.Fatalf("existing evidence changed: %v", err)
	}
}

// Under the budget the body is preserved byte-for-byte; the quota fields stay
// false and the index does not claim a preview.
func TestCaptureQuotaKeepsBodyUnderBudget(t *testing.T) {
	cases := t.TempDir()
	quota := NewProjectHTTPQuota(cases, 1<<20)
	capture, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", quotaFlow("flow-small", 128), nil, quota)
	if err != nil {
		t.Fatal(err)
	}
	if capture.QuotaExceeded || capture.PreviewOnly || capture.Truncated {
		t.Fatalf("under-budget capture marked: %+v", capture)
	}
	read, stored, err := ReadCapture(cases, capture.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if read.QuotaExceeded || read.PreviewOnly {
		t.Fatalf("read flags = %+v", read)
	}
	if len(stored.GetResponse().GetBody()) != 128 {
		t.Fatalf("body bytes = %d, want 128", len(stored.GetResponse().GetBody()))
	}
}

// 0 (or negative) means unlimited: existing bytes are irrelevant and the body
// is always preserved.
func TestCaptureQuotaZeroMeansUnlimited(t *testing.T) {
	cases := t.TempDir()
	httpDir := filepath.Join(cases, ".http")
	if err := os.MkdirAll(httpDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(httpDir, "seed.pb"), bytes.Repeat([]byte("x"), 4096), 0600); err != nil {
		t.Fatal(err)
	}
	for _, max := range []int64{0, -1} {
		quota := NewProjectHTTPQuota(cases, max)
		capture, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", quotaFlow("flow-free", 512), nil, quota)
		if err != nil {
			t.Fatal(err)
		}
		if capture.QuotaExceeded {
			t.Fatalf("max %d reported an exceeded quota", max)
		}
		if _, stored, err := ReadCapture(cases, capture.Ref); err != nil || len(stored.GetResponse().GetBody()) != 512 {
			t.Fatalf("max %d did not preserve the body: %v", max, err)
		}
	}
}

// A project that is already over budget still gets its new index+metadata; the
// previously committed captures are never rewritten or deleted to make room.
func TestCaptureQuotaAlreadyOverKeepsOldEvidence(t *testing.T) {
	cases := t.TempDir()
	old, err := SaveCapture(cases, "run-old", "intent-1", "session-old", "event-old", quotaFlow("flow-old", 600), nil)
	if err != nil {
		t.Fatal(err)
	}
	oldSizes := dirBytes(t, cases)
	quota := NewProjectHTTPQuota(cases, 1024) // already exceeded by the old capture
	if used, err := quota.Used(); err != nil || used != oldSizes {
		t.Fatalf("used = %d, %v; want %d", used, err, oldSizes)
	}
	capture, err := SaveCaptureWithQuota(cases, "run-new", "intent-1", "session-new", "event-new", quotaFlow("flow-new", 600), nil, quota)
	if err != nil {
		t.Fatal(err)
	}
	if !capture.QuotaExceeded {
		t.Fatal("over-budget project accepted another body")
	}
	readOld, storedOld, err := ReadCapture(cases, old.Ref)
	if err != nil {
		t.Fatalf("old capture became unreadable: %v", err)
	}
	if readOld.QuotaExceeded || len(storedOld.GetResponse().GetBody()) != 600 {
		t.Fatalf("old capture rewritten: %+v", readOld)
	}
	if got := dirBytes(t, cases); got < oldSizes {
		t.Fatalf("existing evidence shrank: %d < %d", got, oldSizes)
	}
	if _, _, err := ReadCapture(cases, capture.Ref); err != nil {
		t.Fatalf("new quota index is unreadable: %v", err)
	}
}

// A replay of the same stable identity returns the stored (body-less) capture
// instead of an identity conflict, and does not grow the budget twice.
func TestCaptureQuotaReplayReturnsStoredCapture(t *testing.T) {
	cases := t.TempDir()
	quota := NewProjectHTTPQuota(cases, 1) // force every flow over the budget
	flow := quotaFlow("flow-replay", 32)
	first, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", flow, nil, quota)
	if err != nil {
		t.Fatal(err)
	}
	if !first.QuotaExceeded {
		t.Fatal("fixture did not exceed the quota")
	}
	before := dirBytes(t, cases)
	second, err := SaveCaptureWithQuota(cases, "run-1", "intent-1", "session-1", "event-1", flow, nil, quota)
	if err != nil {
		t.Fatalf("replay conflicted: %v", err)
	}
	if !second.QuotaExceeded || second.Ref != first.Ref {
		t.Fatalf("replay returned %+v, want the stored quota capture", second)
	}
	if after := dirBytes(t, cases); after != before {
		t.Fatalf("replay rewrote the capture: %d -> %d", before, after)
	}
}

// A legacy index row has no quota_exceeded key; it must read as false and stay
// readable. (The key only appears on captures Tianwu itself stripped.)
func TestCaptureQuotaLegacyIndexDefaultsFalse(t *testing.T) {
	cases := t.TempDir()
	capture, err := SaveCapture(cases, "run-legacy", "intent-1", "session-legacy", "event-legacy", quotaFlow("flow-legacy", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	read, _, err := ReadCapture(cases, capture.Ref)
	if err != nil || read.QuotaExceeded {
		t.Fatalf("fresh capture: quota %v, %v", read.QuotaExceeded, err)
	}
	path := captureBase(cases, capture.Ref) + ".json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "quota_exceeded")
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	read, stored, err := ReadCapture(cases, capture.Ref)
	if err != nil {
		t.Fatalf("legacy index unreadable: %v", err)
	}
	if read.QuotaExceeded || read.PreviewOnly {
		t.Fatalf("legacy index gained flags: %+v", read)
	}
	if len(stored.GetResponse().GetBody()) != 64 {
		t.Fatalf("legacy body lost: %d", len(stored.GetResponse().GetBody()))
	}
	rows, total, err := ListCaptures(cases, "", "", 0, 10)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].QuotaExceeded {
		t.Fatalf("ListCaptures = %+v, %d, %v", rows, total, err)
	}
}
