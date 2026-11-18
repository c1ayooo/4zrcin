package traffic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kksty/tianwu/internal/aop/operation"
	native "github.com/kksty/tianwu/internal/aop/traffic"
	"google.golang.org/protobuf/proto"
)

// Capture preserves an observed exchange. Attribution comes from the event's
// accepted run and typed operation reference, never from URL/time guessing.
type Capture struct {
	Ref       string    `json:"ref"`
	RunID     string    `json:"run_id"`
	IntentID  string    `json:"intent_id"`
	SessionID string    `json:"session_id,omitempty"`
	EventID   string    `json:"event_id"`
	FlowID    string    `json:"flow_id"`
	Method    string    `json:"method"`
	URL       string    `json:"url"`
	Status    int32     `json:"status"`
	At        time.Time `json:"at"`
	Complete  bool      `json:"complete"`
	Error     string    `json:"error,omitempty"`
	// PreviewOnly means the body bytes were never persisted by the scan node
	// (local body storage disabled or unavailable), so the console must label
	// what it shows as a bounded preview, not the exchange's original bytes.
	PreviewOnly bool `json:"preview_only"`
	// Truncated means the stored/forwarded body is shorter than the observed
	// one. Both flags are derived from the harness's Flow.Error text ("body
	// truncated (x/y bytes retained)", "(preview only; …)"); legacy captures
	// whose JSON predates the fields are read as false and reconciled from the
	// preserved Error text.
	Truncated bool `json:"truncated"`
	// QuotaExceeded marks an observation whose body bytes were deliberately not
	// preserved because the project's <CaseRoot>/.http evidence budget was full.
	// The index keeps the observed sizes (RequestBytes/ResponseBytes) and the
	// stored Flow keeps headers/metadata, but both bodies are empty for this one
	// observation; older captures are never rewritten to reclaim budget. Legacy
	// JSON without the key reads as false.
	QuotaExceeded bool           `json:"quota_exceeded"`
	RequestBytes  int            `json:"request_bytes"`
	ResponseBytes int            `json:"response_bytes"`
	PayloadSHA256 string         `json:"payload_sha256,omitempty"`
	Operation     *operation.Ref `json:"operation,omitempty"`
}

func captureBase(cases, ref string) string {
	h := sha256.Sum256([]byte(ref))
	return filepath.Join(cases, ".http", hex.EncodeToString(h[:]))
}

func SaveCapture(cases, runID, intentID, sessionID, eventID string, flow *native.Flow, ref *operation.Ref) (Capture, error) {
	return saveCapture(cases, runID, intentID, sessionID, eventID, flow, ref, nil)
}

// SaveCaptureWithQuota is SaveCapture under the project's original-body budget
// (see ProjectHTTPQuota). A max <= 0 is unlimited.
//
// The budget only governs the body bytes. When the incoming flow would push the
// project over it, the observation is still recorded: the index keeps
// method/url/status and the observed body sizes, the stored Flow keeps its
// headers and metadata, but both bodies are emptied for that one new
// observation. The capture is marked QuotaExceeded/PreviewOnly (the console must
// not present it as the original exchange), its Error names used/limit bytes,
// and the caller writes the operator-facing kind=traffic_quota journal warning.
//
// Existing captures are never deleted, rewritten or re-read to reclaim budget. A
// replay of the same stable identity returns the stored capture (possibly
// stripped) instead of conflicting on the deliberately missing bodies.
func SaveCaptureWithQuota(cases, runID, intentID, sessionID, eventID string, flow *native.Flow, ref *operation.Ref, quota *ProjectHTTPQuota) (Capture, error) {
	if quota.Unlimited() {
		return SaveCapture(cases, runID, intentID, sessionID, eventID, flow, ref)
	}
	// One lock covers the budget check, the write and the cached delta: two
	// concurrent flows cannot both see the same free bytes and then exceed it.
	quota.mu.Lock()
	defer quota.mu.Unlock()
	return saveCapture(cases, runID, intentID, sessionID, eventID, flow, ref, quota)
}

// saveCapture is SaveCapture's implementation. When quota is non-nil it is
// already locked by SaveCaptureWithQuota.
func saveCapture(cases, runID, intentID, sessionID, eventID string, flow *native.Flow, ref *operation.Ref, quota *ProjectHTTPQuota) (Capture, error) {
	if flow == nil || flow.Id == "" || runID == "" {
		return Capture{}, fmt.Errorf("HTTP 观察缺少稳定身份")
	}
	r := Capture{Ref: runID + "/" + flow.Id, RunID: runID, IntentID: intentID, SessionID: sessionID, EventID: eventID,
		FlowID: flow.Id, Method: flow.GetRequest().GetMethod(), URL: flow.GetRequest().GetUrl(), Status: flow.GetResponse().GetStatusCode(),
		Complete: flow.Complete, Error: flow.Error, RequestBytes: len(flow.GetRequest().GetBody()), ResponseBytes: len(flow.GetResponse().GetBody()), Operation: ref}
	r.deriveBodyFlags()
	if flow.Timestamp != nil {
		r.At = flow.Timestamp.AsTime()
	}
	if old, stored, err := ReadCapture(cases, r.Ref); err == nil {
		if !sameCapture(old, stored, flow, intentID, sessionID, ref) {
			return Capture{}, fmt.Errorf("HTTP 观察身份冲突：%s", r.Ref)
		}
		return old, nil
	} else if !os.IsNotExist(err) {
		return Capture{}, err
	}
	base := captureBase(cases, r.Ref)
	// A committed index with a missing body is damaged data, not permission to
	// replace the first observation under the same stable identity.
	if _, err := os.Stat(base + ".json"); err == nil {
		return Capture{}, fmt.Errorf("HTTP 原件丢失，保留已有观察：%s", r.Ref)
	} else if !os.IsNotExist(err) {
		return Capture{}, err
	}
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return Capture{}, err
	}

	// The payload written to disk is normally the incoming flow. Under an
	// exceeded quota it becomes a body-less copy of *this* observation; already
	// committed files are never touched.
	stored := flow
	payload, err := proto.Marshal(flow)
	if err != nil {
		return Capture{}, err
	}
	sum := sha256.Sum256(payload)
	r.PayloadSHA256 = hex.EncodeToString(sum[:])
	if quota != nil {
		used, usedErr := quota.usedLocked()
		switch {
		case usedErr != nil:
			// A broken walk must not cost the observation: keep the original
			// bytes and say on the index that the budget could not be checked.
			r.Error = appendFlowError(r.Error, "项目 HTTP 正文配额检查失败，本次未裁剪正文："+usedErr.Error())
		case quota.max > 0:
			meta, err := json.Marshal(r)
			if err != nil {
				return Capture{}, err
			}
			if used+int64(len(payload)+len(meta)) > quota.max {
				stored, r = stripForQuota(flow, r, used, quota.max)
				if payload, err = proto.Marshal(stored); err != nil {
					return Capture{}, err
				}
				sum = sha256.Sum256(payload)
				r.PayloadSHA256 = hex.EncodeToString(sum[:])
			}
		}
	}
	meta, err := json.Marshal(r)
	if err != nil {
		return Capture{}, err
	}
	if err := writeCaptureFile(base+".pb", payload); err != nil {
		quota.invalidateLocked() // nil-safe; a partial write must not under-count
		return Capture{}, err
	}
	if err := writeCaptureFile(base+".json", meta); err != nil {
		quota.invalidateLocked()
		return Capture{}, err
	}
	quota.addLocked(int64(len(payload) + len(meta)))
	return r, nil
}

// sameCapture reports whether a committed capture is the same observation as the
// incoming flow. A quota-stripped capture intentionally has no bodies, so its
// index metadata is the identity instead of proto equality; otherwise a replay
// of the same event would look like an identity conflict.
func sameCapture(old Capture, stored, flow *native.Flow, intentID, sessionID string, ref *operation.Ref) bool {
	if old.IntentID != intentID || old.SessionID != sessionID || !proto.Equal(old.Operation, ref) {
		return false
	}
	if old.QuotaExceeded {
		return old.FlowID == flow.Id &&
			old.Method == flow.GetRequest().GetMethod() &&
			old.URL == flow.GetRequest().GetUrl() &&
			old.Status == flow.GetResponse().GetStatusCode() &&
			old.RequestBytes == len(flow.GetRequest().GetBody()) &&
			old.ResponseBytes == len(flow.GetResponse().GetBody())
	}
	return proto.Equal(stored, flow)
}

// stripForQuota returns the body-less copy written to disk plus the updated
// index. Headers, URL, method, status, timestamp and the operation reference are
// preserved; only Request.Body and Response.Body are dropped, and the reason is
// recorded on both the index and the stored flow.
func stripForQuota(flow *native.Flow, r Capture, used, max int64) (*native.Flow, Capture) {
	notice := fmt.Sprintf("项目 HTTP 正文配额已满（已用 %d / 上限 %d 字节；仅保存索引与元数据，正文未落盘）", used, max)
	stored := proto.CloneOf(flow)
	if stored.Request != nil {
		stored.Request.Body = nil
	}
	if stored.Response != nil {
		stored.Response.Body = nil
	}
	stored.Complete = false
	stored.Error = appendFlowError(stored.Error, notice)
	r.QuotaExceeded = true
	r.PreviewOnly = true
	r.Truncated = true
	r.Complete = false
	r.Error = appendFlowError(r.Error, notice)
	return stored, r
}

// appendFlowError joins a Tianwu notice onto the harness's own flow error prose
// without dropping it: the index must keep both the capture problem and the
// budget decision that omitted the body.
func appendFlowError(base, notice string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return notice
	}
	return base + "；" + notice
}

// deriveBodyFlags translates the harness's own flow error prose into the two
// booleans the console needs. Keep this the only derivation: Save, Read and List
// all call it, so a legacy index row (whose JSON has no flags) still reports the
// truth already present in its preserved Error text. An explicit quota marker
// always wins: the body is absent by Tianwu's own decision, whatever the harness
// prose says.
func (r *Capture) deriveBodyFlags() {
	r.PreviewOnly = strings.Contains(r.Error, "preview only")
	r.Truncated = strings.Contains(r.Error, "body truncated")
	if r.QuotaExceeded {
		r.PreviewOnly = true
		r.Truncated = true
	}
}

func writeCaptureFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".http-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func ReadCapture(cases, ref string) (Capture, *native.Flow, error) {
	if ref == "" || len(ref) > 4096 {
		return Capture{}, nil, fmt.Errorf("无效流量引用")
	}
	base := captureBase(cases, ref)
	b, err := os.ReadFile(base + ".json")
	if err != nil {
		return Capture{}, nil, err
	}
	var r Capture
	if err := json.Unmarshal(b, &r); err != nil {
		return r, nil, err
	}
	if r.Ref != ref {
		return r, nil, fmt.Errorf("流量身份不匹配")
	}
	r.deriveBodyFlags()
	b, err = os.ReadFile(base + ".pb")
	if err != nil {
		return r, nil, err
	}
	if r.PayloadSHA256 != "" {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != r.PayloadSHA256 {
			return r, nil, fmt.Errorf("HTTP 原件与已保存校验值不一致")
		}
	}
	f := new(native.Flow)
	if err := proto.Unmarshal(b, f); err != nil {
		return r, nil, err
	}
	// A quota-stripped observation deliberately stores empty bodies while the
	// index keeps the *observed* sizes as metadata, so the raw-byte comparison
	// cannot apply to those captures.
	requestBytes, responseBytes := r.RequestBytes, r.ResponseBytes
	if r.QuotaExceeded {
		requestBytes, responseBytes = 0, 0
	}
	if f.Id != r.FlowID || r.Ref != r.RunID+"/"+f.Id || f.GetRequest().GetUrl() != r.URL || f.GetRequest().GetMethod() != r.Method || f.GetResponse().GetStatusCode() != r.Status || len(f.GetRequest().GetBody()) != requestBytes || len(f.GetResponse().GetBody()) != responseBytes {
		return r, nil, fmt.Errorf("HTTP 原件与索引身份或摘要不一致")
	}
	return r, f, nil
}

func ListCaptures(cases, query, run string, offset, limit int) ([]Capture, int, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows := []Capture{}
	files, err := os.ReadDir(filepath.Join(cases, ".http"))
	if os.IsNotExist(err) {
		return rows, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cases, ".http", file.Name()))
		if err != nil {
			return nil, 0, err
		}
		var r Capture
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, 0, err
		}
		// Legacy index rows carry no flags; the preserved Error text still says
		// whether the body was a preview or truncated, so derive them here too.
		r.deriveBodyFlags()
		if run != "" && r.RunID != run {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(r.Method+" "+r.URL+" "+fmt.Sprint(r.Status)+" "+r.Error+" "+r.IntentID), query) {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At.Equal(rows[j].At) {
			return rows[i].Ref < rows[j].Ref
		}
		return rows[i].At.After(rows[j].At)
	})
	total := len(rows)
	if offset >= total {
		return []Capture{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return rows[offset:end], total, nil
}
