package store

// The fence-facing asset table (spec #1 工单2). Assets are control-state
// bookkeeping in the same class as operator requests and external actions:
// durable project state, but not findings — board, report, review and the
// content digest never read them. Writes follow the action pattern
// (flushNoRev): a fence row landing or a membership flip is bookkeeping, not
// a change to the fact/intent graph, and waking the commander for it would
// burn a model round per import.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// ErrAssetMissing reports an id that names no row; the API layer maps it.
var ErrAssetMissing = errors.New("资产不存在")

// maxAssetKey bounds one identity (a bare domain is at most 253 bytes; a CIDR
// or host:port is far shorter). The instruction asset_ref shares the cap: it
// names exactly this identity space.
const maxAssetKey = 253

// validateAsset checks one row the way every write path sees it. Identity
// (kind/key) and membership (fence/source) are closed sets: a row the store
// cannot interpret honestly would later read as a fence decision.
func validateAsset(a model.Asset) error {
	if !model.IsAssetKind(a.Kind) {
		return fmt.Errorf("资产类别只能是 domain/host/service/range（收到 %q）", a.Kind)
	}
	if a.Key == "" || len(a.Key) > maxAssetKey {
		return fmt.Errorf("资产标识必填且不超过 %d 字节", maxAssetKey)
	}
	switch a.Fence {
	case model.AssetFenceIn, model.AssetFencePending, model.AssetFenceDenied:
	default:
		return fmt.Errorf("围栏状态只能是 in_fence/pending/denied（收到 %q）", a.Fence)
	}
	switch a.Source {
	case model.AssetSourceFence, model.AssetSourceImport, model.AssetSourceObserved:
	default:
		return fmt.Errorf("资产来源只能是 fence/import/observed（收到 %q）", a.Source)
	}
	return nil
}

// UpsertAsset adds one asset or, when (Kind, Key) already exists, updates the
// mutable fields on that row and returns it with created=false. The creation
// time is the identity's: a row re-seeded by a later flow does not look older
// or newer than the decision that first named it.
func (s *Store) UpsertAsset(a model.Asset) (model.Asset, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Key = strings.TrimSpace(a.Key)
	if err := validateAsset(a); err != nil {
		return model.Asset{}, false, err
	}

	now := time.Now()
	for i := range s.db.Assets {
		existing := &s.db.Assets[i]
		if existing.Kind == a.Kind && existing.Key == a.Key {
			existing.Target, existing.Fence, existing.Source = a.Target, a.Fence, a.Source
			existing.UpdatedAt = now
			out := *existing
			if err := s.flushNoRev("asset"); err != nil {
				return model.Asset{}, false, err
			}
			return out, false, nil
		}
	}
	a.ID = s.uniqueIDLocked("as")
	a.CreatedAt = now
	a.UpdatedAt = now
	s.db.Assets = append(s.db.Assets, a)
	if err := s.flushNoRev("asset"); err != nil {
		return model.Asset{}, false, err
	}
	return a, true, nil
}

// UpdateAsset mutates one row through fn. An unknown id is an error, not a
// silent no-op: a fence decision that "succeeded" against nothing would hide
// its own failure. The mutation may not change the row's identity (kind/key —
// rebinding an identity is an upsert of the new one, not an edit) and the
// result is re-validated before it can commit; an invalid mutation restores
// the row instead of landing a half-written state.
func (s *Store) UpdateAsset(id string, fn func(*model.Asset)) (model.Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.db.Assets {
		if s.db.Assets[i].ID != id {
			continue
		}
		before := s.db.Assets[i]
		fn(&s.db.Assets[i])
		s.db.Assets[i].UpdatedAt = time.Now()
		if s.db.Assets[i].Kind != before.Kind || s.db.Assets[i].Key != before.Key {
			s.db.Assets[i] = before
			return model.Asset{}, fmt.Errorf("%w：身份字段（kind/key）不可改，改绑请对新身份 Upsert", ErrAssetMissing)
		}
		if err := validateAsset(s.db.Assets[i]); err != nil {
			s.db.Assets[i] = before
			return model.Asset{}, err
		}
		out := s.db.Assets[i]
		if err := s.flushNoRev("asset"); err != nil {
			return model.Asset{}, err
		}
		return out, nil
	}
	return model.Asset{}, fmt.Errorf("%w：%s", ErrAssetMissing, id)
}

// DeleteAsset removes one row (a mis-imported identity has no lifecycle that
// withdrawing a fact would serve). The delete is durable: a restart does not
// resurrect the row.
func (s *Store) DeleteAsset(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.db.Assets {
		if s.db.Assets[i].ID != id {
			continue
		}
		s.db.Assets = append(s.db.Assets[:i], s.db.Assets[i+1:]...)
		return s.flushNoRev("asset")
	}
	return fmt.Errorf("%w：%s", ErrAssetMissing, id)
}

// ListAssets returns every row in the stable (kind, key) order, so a reload
// does not reshuffle the table the operator is reading.
func (s *Store) ListAssets() []model.Asset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Asset, len(s.db.Assets))
	copy(out, s.db.Assets)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// GetAsset resolves one row by id.
func (s *Store) GetAsset(id string) (model.Asset, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.db.Assets {
		if a.ID == id {
			return a, true
		}
	}
	return model.Asset{}, false
}

// AssetByKindKey resolves the row an identity names, for flows that re-derive
// membership (an import, a fence edit) instead of carrying ids around.
func (s *Store) AssetByKindKey(kind, key string) (model.Asset, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key = strings.TrimSpace(key)
	for _, a := range s.db.Assets {
		if a.Kind == kind && a.Key == key {
			return a, true
		}
	}
	return model.Asset{}, false
}
