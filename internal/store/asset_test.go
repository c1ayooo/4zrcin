package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

func fencedAsset(key string) model.Asset {
	return model.Asset{Kind: model.AssetKindDomain, Key: key, Target: true,
		Fence: model.AssetFenceIn, Source: model.AssetSourceFence}
}

// Upsert is the fence-membership write: one identity (kind + key) is one asset
// no matter how many flows add it. Before this contract the same host could
// enter the table once per source and the fence state would depend on which
// row the reader happened to see.
func TestAssetUpsertIsIdempotentByIdentity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first, created, err := s.UpsertAsset(fencedAsset("target.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.ID == "" || !strings.HasPrefix(first.ID, "as_") {
		t.Fatalf("first upsert = created=%v id=%q", created, first.ID)
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("created asset carries no timestamp")
	}

	second, created, err := s.UpsertAsset(model.Asset{
		Kind: model.AssetKindDomain, Key: "target.example",
		Target: true, Fence: model.AssetFencePending, Source: model.AssetSourceObserved,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("the same identity created a second row")
	}
	if second.ID != first.ID {
		t.Fatalf("identity changed: %q -> %q", first.ID, second.ID)
	}
	if second.Fence != model.AssetFencePending || second.Source != model.AssetSourceObserved {
		t.Fatalf("upsert did not take the newer membership: %+v", second)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("upsert reset the creation time: %v -> %v", first.CreatedAt, second.CreatedAt)
	}
	if len(s.ListAssets()) != 1 {
		t.Fatalf("ListAssets = %d rows, want 1", len(s.ListAssets()))
	}
}

// A row the store cannot interpret honestly must be refused before it lands:
// a wrong kind or membership would read as a real fence decision later.
func TestAssetValidationRejectsImpossibleRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cases := []model.Asset{
		{Kind: "cluster", Key: "a.example", Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "", Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: strings.Repeat("a.", 130) + "example", Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "a.example", Fence: "maybe", Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "a.example", Fence: model.AssetFenceIn, Source: "guess"},
		{Kind: model.AssetKindDomain, Key: "a.example", Target: true, Source: model.AssetSourceFence},
	}
	for i, a := range cases {
		if _, _, err := s.UpsertAsset(a); err == nil {
			t.Fatalf("case %d: impossible asset accepted: %+v", i, a)
		}
	}
	if len(s.ListAssets()) != 0 {
		t.Fatalf("rejected rows landed anyway: %d", len(s.ListAssets()))
	}
}

// Assets are durable project state like any ledger object: a restart keeps
// them, the list is stable (kind, then key), and both lookups resolve.
func TestAssetsSurviveRestartAndListStably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seed := []model.Asset{
		{Kind: model.AssetKindRange, Key: "10.0.0.0/8", Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "b.example", Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindDomain, Key: "a.example", Target: true, Fence: model.AssetFenceIn, Source: model.AssetSourceFence},
		{Kind: model.AssetKindHost, Key: "10.0.0.5", Target: false, Fence: model.AssetFencePending, Source: model.AssetSourceObserved},
	}
	var id string
	for _, a := range seed {
		row, _, err := s.UpsertAsset(a)
		if err != nil {
			t.Fatal(err)
		}
		if a.Key == "b.example" {
			id = row.ID
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	rows := reopened.ListAssets()
	if len(rows) != len(seed) {
		t.Fatalf("rows = %d, want %d", len(rows), len(seed))
	}
	// domain < host < range, keys ascending inside one kind.
	want := []string{"a.example", "b.example", "10.0.0.5", "10.0.0.0/8"}
	for i, key := range want {
		if rows[i].Key != key {
			t.Fatalf("row %d = %q, want %q (list not stable)", i, rows[i].Key, key)
		}
	}
	if got, ok := reopened.GetAsset(id); !ok || got.Key != "b.example" {
		t.Fatalf("GetAsset(%q) = %+v ok=%v", id, got, ok)
	}
	if got, ok := reopened.AssetByKindKey(model.AssetKindRange, "10.0.0.0/8"); !ok || got.ID != rows[3].ID {
		t.Fatalf("AssetByKindKey resolved %+v ok=%v", got, ok)
	}
	if _, ok := reopened.GetAsset("as_missing"); ok {
		t.Fatal("a missing id resolved")
	}
}

// Fence membership changes go through the generic update; an unknown id is an
// error, not a silent no-op. The mutation may not rebind the identity, and an
// invalid result restores the row instead of landing a half-written state.
func TestUpdateAssetMovesMembership(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	row, _, err := s.UpsertAsset(fencedAsset("target.example"))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateAsset(row.ID, func(a *model.Asset) {
		a.Fence = model.AssetFenceDenied
		a.Source = model.AssetSourceObserved
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Fence != model.AssetFenceDenied || updated.Key != row.Key {
		t.Fatalf("update = %+v", updated)
	}
	if _, err := s.UpdateAsset("as_missing", func(a *model.Asset) { a.Fence = model.AssetFenceIn }); !errors.Is(err, ErrAssetMissing) {
		t.Fatalf("updating a missing asset = %v, want ErrAssetMissing", err)
	}
	// Identity is not editable through the update surface: rebinding is an
	// upsert of the new identity, never a silent rewrite of the old row.
	if _, err := s.UpdateAsset(row.ID, func(a *model.Asset) { a.Key = "other.example" }); err == nil {
		t.Fatal("the update rebinded the identity")
	}
	// An invalid membership restores the row: a rejected write must not leave
	// the fence state the caller's error already contradicts.
	if _, err := s.UpdateAsset(row.ID, func(a *model.Asset) { a.Fence = "maybe" }); err == nil {
		t.Fatal("an impossible fence state was accepted")
	}
	if got, _ := s.GetAsset(row.ID); got.Fence != model.AssetFenceDenied {
		t.Fatalf("rejected mutation left the row changed: %+v", got)
	}
}

// A snapshot owns its rows: a later upsert mutates the live table, never the
// slices an earlier Snapshot() handed out.
func TestSnapshotAssetsAreDetached(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, _, err := s.UpsertAsset(fencedAsset("target.example")); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Assets) != 1 || snap.Assets[0].Fence != model.AssetFenceIn {
		t.Fatalf("snapshot = %+v", snap.Assets)
	}
	if _, _, err := s.UpsertAsset(model.Asset{
		Kind: model.AssetKindDomain, Key: "target.example",
		Target: true, Fence: model.AssetFencePending, Source: model.AssetSourceObserved,
	}); err != nil {
		t.Fatal(err)
	}
	if snap.Assets[0].Fence != model.AssetFenceIn {
		t.Fatalf("the live write aliased the snapshot: %+v", snap.Assets[0])
	}
}

// A mis-imported identity is deletable; the delete is durable and an unknown
// id reports ErrAssetMissing instead of pretending to succeed.
func TestDeleteAssetRemovesTheRowDurably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	kept, _, err := s.UpsertAsset(fencedAsset("kept.example"))
	if err != nil {
		t.Fatal(err)
	}
	gone, _, err := s.UpsertAsset(fencedAsset("wrong.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(gone.ID); !errors.Is(err, ErrAssetMissing) {
		t.Fatalf("repeat delete = %v, want ErrAssetMissing", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows := reopened.ListAssets()
	if len(rows) != 1 || rows[0].ID != kept.ID {
		t.Fatalf("rows after delete = %+v", rows)
	}
	if _, ok := reopened.GetAsset(gone.ID); ok {
		t.Fatal("the deleted row survived a restart")
	}
}
