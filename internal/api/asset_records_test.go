package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// The records endpoint is the store projection the asset page builds on: rows
// come back in the store's stable order, and an empty table is an empty list,
// never null.
func TestAssetRecordsEndpointListsStoredRows(t *testing.T) {
	s, _ := newBindingServer(t)
	if rec := call(t, s, http.MethodGet, "/api/assets/records", nil); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"assets":[]}` {
		t.Fatalf("empty table = %d %s", rec.Code, rec.Body.String())
	}
	if _, _, err := s.Store.UpsertAsset(model.Asset{
		Kind: model.AssetKindDomain, Key: "target.example", Target: true,
		Fence: model.AssetFenceIn, Source: model.AssetSourceFence,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Store.UpsertAsset(model.Asset{
		Kind: model.AssetKindRange, Key: "10.0.0.0/8", Target: true,
		Fence: model.AssetFenceIn, Source: model.AssetSourceFence,
	}); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, http.MethodGet, "/api/assets/records", nil)
	if rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Assets []model.Asset `json:"assets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Assets) != 2 || out.Assets[0].Kind != model.AssetKindDomain || out.Assets[1].Kind != model.AssetKindRange {
		t.Fatalf("records = %+v", out.Assets)
	}
}
