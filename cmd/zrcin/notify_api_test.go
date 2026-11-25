package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/notify"
)

// notifyHubFixture 构造一个只挂通知组件的 hub：通知 handler 只依赖这两个字段。
func notifyHubFixture(t *testing.T) *hub {
	t.Helper()
	st, err := notify.OpenStore(filepath.Join(t.TempDir(), "notify.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &hub{notifyStore: st, notifier: notify.NewEngine(st, nil, nil)}
}

func notifyRequest(t *testing.T, h *hub, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	h.serveNotify(rec, req)
	return rec
}

func TestNotifyChannelCRUDAndMasking(t *testing.T) {
	h := notifyHubFixture(t)
	secret := "https://oapi.dingtalk.com/robot/send?access_token=supersecret"

	// 新建：必填缺失被拒。
	if rec := notifyRequest(t, h, http.MethodPost, "/api/notify/channels",
		map[string]any{"name": "", "kind": "dingtalk", "config": map[string]any{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("空名称应 400，得到 %d", rec.Code)
	}
	// 新建：非法 kind 被拒。
	if rec := notifyRequest(t, h, http.MethodPost, "/api/notify/channels",
		map[string]any{"name": "x", "kind": "nope", "config": map[string]any{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 kind 应 400，得到 %d", rec.Code)
	}
	// 新建：合法 dingtalk 通道。
	rec := notifyRequest(t, h, http.MethodPost, "/api/notify/channels", map[string]any{
		"name": "告警群", "kind": "dingtalk", "enabled": true,
		"config": map[string]any{"webhook": secret, "secret": "SECxxx"},
		"events": []string{"vuln_recorded"}, "min_severity": "high",
		"mode": "realtime",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("新建应 200，得到 %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string         `json:"id"`
		Config map[string]any `json:"config"`
		Secret []string       `json:"secret_keys"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == "" || len(created.Secret) == 0 {
		t.Fatalf("应返回 id 与 secret_keys：%s", rec.Body.String())
	}
	// 回显必须是掩码，不能把凭据原样吐给浏览器。
	if got, _ := created.Config["webhook"].(string); got == secret {
		t.Fatal("webhook 凭据未掩码")
	}

	// 更新：掩码值原样回传=保留原值（凭据不丢）。
	rec = notifyRequest(t, h, http.MethodPut, "/api/notify/channels/"+created.ID, map[string]any{
		"name": "告警群2", "enabled": true, "mode": "digest",
		"config": created.Config, "events": []string{"vuln_recorded", "run_failed"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应 200，得到 %d：%s", rec.Code, rec.Body.String())
	}
	stored := h.notifyStore.View().Channels[0]
	if stored.Name != "告警群2" || stored.Mode != notify.ModeDigest {
		t.Fatalf("更新未生效：%+v", stored)
	}
	if got, _ := stored.Config["webhook"].(string); got != secret {
		t.Fatal("掩码回传应保留库中原凭据")
	}

	// 删除。
	rec = notifyRequest(t, h, http.MethodDelete, "/api/notify/channels/"+created.ID, nil)
	if rec.Code != http.StatusOK || len(h.notifyStore.View().Channels) != 0 {
		t.Fatalf("删除应 200 且清空列表，得到 %d", rec.Code)
	}
}

func TestNotifyChannelUpdateRejectsDestinationSwapWithoutSecrets(t *testing.T) {
	h := notifyHubFixture(t)
	rec := notifyRequest(t, h, http.MethodPost, "/api/notify/channels", map[string]any{
		"name": "w", "kind": "dingtalk", "enabled": true,
		"config": map[string]any{"webhook": "https://a.example/robot/send?access_token=x", "secret": "SEC1"},
	})
	var created struct {
		ID     string         `json:"id"`
		Config map[string]any `json:"config"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	// 只改目标地址、凭据保持掩码：必须被拒，否则真凭据会被发到新地址。
	rec = notifyRequest(t, h, http.MethodPut, "/api/notify/channels/"+created.ID, map[string]any{
		"enabled": true, "config": map[string]any{
			"webhook": "https://evil.example/robot/send?access_token=y", "secret": created.Config["secret"],
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("改地址不重新表态凭据应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestNotifyMetaAndDeliveries(t *testing.T) {
	h := notifyHubFixture(t)
	rec := notifyRequest(t, h, http.MethodGet, "/api/notify/meta", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta 应 200，得到 %d", rec.Code)
	}
	var meta struct {
		EventKinds []map[string]string `json:"event_kinds"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &meta)
	if len(meta.EventKinds) == 0 {
		t.Fatal("meta 应下发事件类型枚举")
	}
	rec = notifyRequest(t, h, http.MethodGet, "/api/notify/deliveries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("deliveries 应 200，得到 %d", rec.Code)
	}
}
