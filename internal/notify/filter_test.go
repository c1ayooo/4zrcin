package notify

import (
	"encoding/json"
	"testing"
)

// ev 构造一条带级别的事件，供门槛测试使用。
func ev(sev string) Snapshot {
	return Snapshot{Kind: EventVulnRecorded, Severity: sev}
}

func TestParseFilterNeverErrors(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("not json"), []byte(`{"min_severity":123}`)} {
		f := ParseFilter(raw)
		// 畸形配置一律退化为零值 Filter（= 不限 = 全部命中）：宁可多推，不可静默漏推。
		if f.MinSeverity != "" || len(f.Events) != 0 {
			t.Fatalf("畸形配置应退化为零值 Filter，得到 %+v", f)
		}
	}
}

func TestMatchEventSelection(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		kind   string
		want   bool
	}{
		{"空勾选表示不限", nil, EventIntentFailed, true},
		{"勾选命中", []string{EventIntentFailed, EventRunFailed}, EventIntentFailed, true},
		{"勾选未命中", []string{EventRunFailed}, EventIntentFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(Filter{Events: tc.events}, Snapshot{Kind: tc.kind}); got != tc.want {
				t.Fatalf("Match=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchMinSeverityGate(t *testing.T) {
	cases := []struct {
		min  string
		sev  string
		want bool
	}{
		{"", "low", true},
		{"", "critical", true},
		{"medium", "low", false},
		{"medium", "medium", true},
		{"critical", "high", false},
		// 未知级别序数为 0，被任何非空门槛挡住：存疑时不推。
		{"medium", "weird", false},
	}
	for _, tc := range cases {
		if got := Match(Filter{MinSeverity: tc.min}, ev(tc.sev)); got != tc.want {
			t.Fatalf("min=%q sev=%q: Match=%v, want %v", tc.min, tc.sev, got, tc.want)
		}
	}
}

// 级别门槛只应筛带级别的事件：无级别语义的事件（意图失败等）不应被门槛挡掉。
func TestMatchSeverityGateSkipsUnseverityEvents(t *testing.T) {
	if !Match(Filter{MinSeverity: "critical"}, Snapshot{Kind: EventIntentFailed}) {
		t.Fatal("无级别事件不应被 min_severity 门槛挡住")
	}
}

func TestFilterValidateRejectsUnknownSeverity(t *testing.T) {
	if err := (Filter{MinSeverity: "hgih"}).Validate(); err == nil {
		t.Fatal("拼错的门槛应在写入时被拦下，否则会静默失效变成全推")
	}
	if err := (Filter{MinSeverity: "critical"}).Validate(); err != nil {
		t.Fatalf("合法门槛不应报错: %v", err)
	}
}

func TestParseFilterKeepsJSONRoundTrip(t *testing.T) {
	raw, err := json.Marshal(Filter{Events: []string{EventVulnRecorded}, MinSeverity: "high"})
	if err != nil {
		t.Fatal(err)
	}
	f := ParseFilter(raw)
	if len(f.Events) != 1 || f.Events[0] != EventVulnRecorded || f.MinSeverity != "high" {
		t.Fatalf("往返不一致: %+v", f)
	}
}

func TestEventMetadata(t *testing.T) {
	if !ValidEvent(EventVulnRecorded) || ValidEvent("nope") {
		t.Fatal("ValidEvent 判定错误")
	}
	if !ValidEvents([]string{EventVulnRecorded, EventRunFailed}) || ValidEvents([]string{"nope"}) {
		t.Fatal("ValidEvents 判定错误")
	}
	if EventLabel("nope") != "nope" {
		t.Fatal("未知事件类型应原样回显")
	}
}
