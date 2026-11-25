package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// engineFixture 起一个假接收端与临时配置，返回可直接 step 的引擎。
func engineFixture(t *testing.T, events []string, minSeverity string) (*Engine, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		buf.Append(raw)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	st, err := OpenStore(filepath.Join(t.TempDir(), "notify.json"))
	if err != nil {
		t.Fatal(err)
	}
	st.Save(File{
		Enabled:       true,
		DigestMinutes: DefaultDigestMinutes,
		Channels: []ChannelConfig{{
			ID: "c1", Name: "测试通道", Kind: KindWebhook, Enabled: true,
			Config: map[string]any{"url": srv.URL},
			Events: events, MinSeverity: minSeverity, Mode: ModeRealtime,
		}},
	})
	e := NewEngine(st, func(id string) string { return "项目" + id }, func(s Snapshot) string { return "link://" + s.RefID })
	return e, buf
}

type syncBuffer struct {
	mu   sync.Mutex
	data []map[string]any
}

func (b *syncBuffer) Append(raw []byte) {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	b.mu.Lock()
	b.data = append(b.data, m)
	b.mu.Unlock()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.data)
}

func TestEngineFanOutRespectsEventSelection(t *testing.T) {
	e, buf := engineFixture(t, []string{EventVulnRecorded}, "")
	now := time.Now()
	e.Emit(EventVulnRecorded, "1", "p1", "漏洞一", "", "high")
	e.Emit(EventIntentFailed, "2", "p1", "意图失败", "", "")
	e.step(now)
	if buf.Len() != 1 {
		t.Fatalf("只勾选 vuln_recorded，应只投递 1 条，得到 %d", buf.Len())
	}
	ds := e.Deliveries()
	if len(ds) != 1 || ds[0].State != StateSent {
		t.Fatalf("投递应已送达，得到 %+v", ds)
	}
	if ds[0].Project != "项目p1" {
		t.Fatalf("项目名应由注入的 names 生成，得到 %q", ds[0].Project)
	}
	if ds[0].EventLabel != "漏洞入库" {
		t.Fatalf("事件标签错误: %q", ds[0].EventLabel)
	}
}

func TestEngineFanOutRespectsSeverityGate(t *testing.T) {
	e, buf := engineFixture(t, nil, "critical")
	now := time.Now()
	e.Emit(EventVulnRecorded, "1", "", "中危漏洞", "", "medium")
	e.Emit(EventVulnRecorded, "2", "", "严重漏洞", "", "critical")
	e.step(now)
	if buf.Len() != 1 {
		t.Fatalf("min_severity=critical 应只投递 1 条，得到 %d", buf.Len())
	}
}

func TestEngineRetryFailedDelivery(t *testing.T) {
	e, _ := engineFixture(t, nil, "")
	now := time.Now()
	e.Emit(EventRunFailed, "1", "", "运行失败", "", "")
	e.step(now)
	ds := e.Deliveries()
	if len(ds) != 1 || ds[0].State != StateSent {
		t.Fatalf("webhook 正常时投递应 sent，得到 %+v", ds)
	}
	// 手动重试只对失败记录有效。
	if e.Retry(ds[0].ID) {
		t.Fatal("sent 记录不应可重试")
	}
}

func TestEngineDigestWaitsForWindow(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "notify.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	st.Save(File{
		Enabled: true, DigestMinutes: 30,
		Channels: []ChannelConfig{{
			ID: "d1", Name: "汇总", Kind: KindWebhook, Enabled: true,
			Config: map[string]any{"url": srv.URL}, Mode: ModeDigest,
		}},
	})
	e := NewEngine(st, nil, nil)
	now := time.Now()
	e.Emit(EventVulnRecorded, "1", "", "a", "", "high")
	e.Emit(EventVulnRecorded, "2", "", "b", "", "high")
	// 周期未到：不投递。
	e.step(now)
	for _, d := range e.Deliveries() {
		if d.State != StatePending {
			t.Fatalf("周期未到应保持 pending，得到 %s", d.State)
		}
	}
	// 周期到了：两条合成一条发出。
	e.step(now.Add(31 * time.Minute))
	sent := 0
	for _, d := range e.Deliveries() {
		if d.State == StateSent {
			sent++
		}
	}
	if sent != 2 {
		t.Fatalf("周期到期后 2 条都应 sent，得到 %d", sent)
	}
}

func TestEngineDisabledGlobalSwitch(t *testing.T) {
	e, buf := engineFixture(t, nil, "")
	e.store.Save(File{Enabled: false, DigestMinutes: DefaultDigestMinutes,
		Channels: e.store.View().Channels})
	e.Emit(EventVulnRecorded, "1", "", "a", "", "high")
	e.step(time.Now())
	if buf.Len() != 0 {
		t.Fatal("全局开关关闭时不应投递")
	}
}
