package api

// The fence-out pending flow (spec #1 工单5). The node refuses an out-of-range
// request and marks the verdict default_missed (fork patch 30); the control
// plane reads that from the capture metadata and makes the ASSET — not the
// request — the operator's decision:
//
//	pending:  asset recorded 围栏外, a parked intent marks "don't test this",
//	          an asset-scoped persistent instruction tells every round so
//	approve:  join the fence (allow row + asset in_fence), revoke the stop
//	          instruction, dispatch a "continue testing" intent
//	reject:   asset denied, instruction rewritten to "不在授权范围"
//
// The approval lives here, not on the request: a request approval could not
// admit the asset, and the node already refused it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// fenceOutInstructionKeys are the idempotency keys of the two instructions a
// pending/denied asset owns: one stable key per (state, identity), so a
// replayed trigger or a double-clicked resolve creates nothing new.
func fenceOutStopKey(kind, key string) string  { return "fence-out:" + kind + ":" + key }
func fenceDeniedKey(kind, key string) string   { return "fence-denied:" + kind + ":" + key }
func fenceContinueKey(kind, key string) string { return "fence-continue:" + kind + ":" + key }

// assetKindForHost classifies one observed request host: an IP literal is a
// host asset, a hostname a domain asset (the registrable-domain grouping is a
// projection concern, not this identity).
func assetKindForHost(host string) string {
	if model.IsIPLiteral(host) {
		return model.AssetKindHost
	}
	return model.AssetKindDomain
}

// observeFenceOut turns one refused out-of-range capture into a pending asset
// (spec #1 工单5/6). It is idempotent by asset identity: the first refusal
// records the pending row, parks the stop intent and activates the
// instruction; every later refusal of the same host is quiet. The caller
// serialises via fenceOutMu, so the check-then-create cannot race itself.
func (s *Server) observeFenceOut(capture traffic.Capture) error {
	if s.Store == nil || s.Project == nil {
		return nil
	}
	parsed, err := url.Parse(capture.URL)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	kind := assetKindForHost(host)

	s.fenceOutMu.Lock()
	defer s.fenceOutMu.Unlock()
	if _, ok := s.Store.AssetByKindKey(kind, host); ok {
		// Any existing row means the decision already exists: pending is
		// queued, in_fence is a stale-rule round, denied was decided. Later
		// refusals write nothing — the queue is one decision per identity.
		return nil
	}
	if _, _, err := s.Store.UpsertAsset(model.Asset{
		Kind: kind, Key: host, Target: true,
		Fence: model.AssetFencePending, Source: model.AssetSourceObserved,
	}); err != nil {
		return err
	}
	// First refusal of this identity: park the stop intent and activate the
	// asset-scoped instruction every round carries.
	in, err := s.Store.CreateIntent(model.Intent{
		Title:       "不再测试这个资产：" + host,
		Description: "范围外请求被拒绝，目标已进入待审；操作员批准后自动入围栏并继续测试。",
		CreatedBy:   collab.OperatorID,
	})
	if err != nil {
		// The pending row is the decision record; the marker intent is
		// bookkeeping. A failed marker must not hide the pending asset.
		log.Printf("fence-out %s: 停放意图创建失败：%v", host, err)
	} else if _, err := s.Store.UpdateIntent(in.ID, func(i *model.Intent) { i.Status = model.StatusParked }); err != nil {
		log.Printf("fence-out %s: 停放意图失败：%v", host, err)
	}
	if _, _, err := s.Store.CreateInstruction(model.Instruction{
		Text:           "不要测试 " + host + "：它在围栏外，请求已被拒绝，等待操作员审批。",
		AssetRef:       host,
		IdempotencyKey: fenceOutStopKey(kind, host),
		CreatedBy:      collab.OperatorID,
	}); err != nil {
		return fmt.Errorf("启用停手指令失败：%w", err)
	}
	return nil
}

// revokeAssetInstructions revokes every active instruction bound to the
// asset: the pending-stop text must not survive a decision, and revocation is
// the instruction lifecycle's own answer (history keeps the text).
func (s *Server) revokeAssetInstructions(assetRef string) {
	for _, in := range s.Store.InstructionsForAsset(assetRef) {
		if in.State != model.InstructionActive {
			continue
		}
		if _, err := s.Store.UpdateInstruction(in.ID, in.Version, in.Text, model.InstructionRevoked); err != nil {
			log.Printf("asset %s: 指令 %s 撤回失败：%v", assetRef, in.ID, err)
		}
	}
}

// joinFence adds the asset's allow row to the project fence and marks the
// asset in_fence. The row grammar is the fence's own: a bare hostname row
// covers the domain and its whole subdomain tree; a CIDR row is a range; an
// IP literal is an exact host. Joining the first asset enables the fence with
// the fixed fallback (review = refuse + pending), never shadow.
func (s *Server) joinFence(asset model.Asset) (model.Asset, error) {
	if asset.Kind == model.AssetKindService {
		return asset, errors.New("围栏按主机加入：service 资产请对其主机操作")
	}
	if asset.Fence == model.AssetFenceIn {
		return asset, nil
	}
	rules, ok := s.Project.ScopeRulesOf()
	if !ok {
		rules = project.ScopeRules{Enabled: true, DefaultAction: project.ScopeActionReview}
	}
	rules.Enabled = true
	if rules.DefaultAction == "" {
		rules.DefaultAction = project.ScopeActionReview
	}
	if !hasAllowRow(rules, asset.Key) {
		rules.Rules = append([]project.ScopeRule{{
			Action: project.ScopeActionRecord, Method: "*", Host: asset.Key, PathPrefix: "/",
		}}, rules.Rules...)
	}
	if err := s.persistScopeRules(&rules); err != nil {
		return asset, err
	}
	return s.Store.UpdateAsset(asset.ID, func(a *model.Asset) {
		a.Fence = model.AssetFenceIn
		a.Source = model.AssetSourceFence
	})
}

// hasAllowRow reports whether the table already admits this host: a same-host
// row with a non-record action (the block a leave prepends) does not count, or
// a rejoin would skip its allow row while the block kept refusing.
func hasAllowRow(rules project.ScopeRules, host string) bool {
	for _, r := range rules.Rules {
		if r.Action == project.ScopeActionRecord && strings.EqualFold(r.Host, host) {
			return true
		}
	}
	return false
}

// persistScopeRules is the manifest write, supplied by the hub (the project
// manager owns the manifest). A bare test server leaves it nil; fence
// membership writes then answer 503 instead of pretending.
func (s *Server) persistScopeRules(rules *project.ScopeRules) error {
	if s.PersistScopeRules == nil {
		return errors.New("围栏写入不可用：项目清单写入未挂载")
	}
	_, err := s.PersistScopeRules(rules)
	return err
}

// leaveFence takes the asset out of the fence: its allow rows are removed;
// when a broader row still admits the host (a parent domain or CIDR), an
// explicit block row is prepended so first-hit-wins actually excludes it.
// The asset turns denied (so later refusals do not re-queue it), the
// directions that tested it are stopped through the operator-scope stop
// record (the durable decision + late-fact annotation), and the guidance is
// rewritten to "不在授权范围".
func (s *Server) leaveFence(asset model.Asset) (model.Asset, error) {
	if asset.Kind == model.AssetKindService {
		return asset, errors.New("围栏按主机移出：service 资产请对其主机操作")
	}
	if asset.Fence == model.AssetFenceDenied {
		// Already out of range: the block row and the stop record exist.
		return asset, nil
	}
	if rules, ok := s.Project.ScopeRulesOf(); ok {
		kept := make([]project.ScopeRule, 0, len(rules.Rules))
		for _, r := range rules.Rules {
			if strings.EqualFold(r.Host, asset.Key) {
				continue
			}
			kept = append(kept, r)
		}
		// Always exclude explicitly: removing the asset's own allow rows
		// leaves a broader row's admission intact (a parent-domain row admits
		// every subdomain, a CIDR row every address in it), and first-hit-wins
		// needs the block above them. A join prepends its allow row ahead of
		// this block, so rejoining works without editing.
		kept = append([]project.ScopeRule{{
			Action: project.ScopeActionBlock, Method: "*", Host: asset.Key, PathPrefix: "/",
		}}, kept...)
		rules.Rules = kept
		if err := s.persistScopeRules(&rules); err != nil {
			return asset, err
		}
	}
	// Stop the directions that were testing it. The operator-scope stop is
	// the durable record; applyOperatorScopeStop cancels exactly the listed
	// (still running) intents, and late facts carry the annotation.
	intentIDs := s.runningIntentsForHost(asset.Key)
	entry, _, err := store.AppendOperatorScope(s.projectDir(), store.OperatorScopeEntry{
		Kind:    store.OperatorScopeKindStop,
		Summary: "移出围栏：" + asset.Key,
		// Two spellings: the bare key is the asset identity, "host:<key>" is
		// the entity_ref convention facts are declared with — late-fact
		// annotation matches strings exactly, so both must be listed.
		EntityRefs: []string{asset.Key, "host:" + asset.Key},
		IntentIDs:  intentIDs,
		Note:       "主机移出测试范围；此后该主机的事实标记为停测后记录。",
	})
	if err != nil {
		return asset, fmt.Errorf("写入停测记录失败：%w", err)
	}
	// Apply the entry this call appended: re-reading the newest stop would
	// let a concurrent /api/operator-scope append win the read and be applied
	// twice while this one never runs.
	s.applyOperatorScopeStop(entry)
	return s.denyAsset(asset)
}

// denyAsset is the shared rejection tail (leave and pending-reject): the asset
// turns denied, the stop guidance is revoked and the exclusion is stated as
// active guidance.
func (s *Server) denyAsset(asset model.Asset) (model.Asset, error) {
	denied, err := s.Store.UpdateAsset(asset.ID, func(a *model.Asset) {
		a.Fence = model.AssetFenceDenied
		a.Source = model.AssetSourceObserved
	})
	if err != nil {
		return asset, err
	}
	s.revokeAssetInstructions(asset.Key)
	if _, _, err := s.Store.CreateInstruction(model.Instruction{
		Text:           asset.Key + " 不在授权范围，不要测试。",
		AssetRef:       asset.Key,
		IdempotencyKey: fenceDeniedKey(asset.Kind, asset.Key),
		CreatedBy:      collab.OperatorID,
	}); err != nil {
		log.Printf("asset %s: 拒绝说明指令创建失败：%v", asset.Key, err)
	}
	return denied, nil
}

// runningIntentsForHost narrows the capture-derived intent list to directions
// that are still running: a finished direction has nothing left to stop, and
// the stop record's intent_ids are the cancellation set.
func (s *Server) runningIntentsForHost(host string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range traffic.IntentsForHost(s.CaseRoot, host) {
		in, ok := s.Store.GetIntent(id)
		if !ok || seen[id] {
			continue
		}
		if in.Status != model.StatusRunning {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// resolvePendingAsset applies one decision. approve joins the fence and
// dispatches the continue intent; reject marks the asset denied and states
// the exclusion in the guidance. Both revoke the pending-stop instruction.
func (s *Server) resolvePendingAsset(asset model.Asset, approve bool) (model.Asset, error) {
	if asset.Fence != model.AssetFencePending {
		return asset, errors.New("该资产不在待审状态（可能已被处理）")
	}
	if approve {
		joined, err := s.joinFence(asset)
		if err != nil {
			return asset, err
		}
		s.revokeAssetInstructions(asset.Key)
		if _, err := s.Store.CreateIntent(model.Intent{
			Title:       "继续测试 " + asset.Key + "：已加入围栏",
			Description: "操作员批准范围外资产加入围栏，恢复对该主机的测试。",
			CreatedBy:   collab.OperatorID,
		}); err != nil {
			log.Printf("asset %s: 继续测试意图创建失败：%v", asset.Key, err)
		}
		return joined, nil
	}
	return s.denyAsset(asset)
}

// ---- HTTP surface ---------------------------------------------------------

// listFencePending returns the pending queue (operator-only: it is the
// fence's decision surface, the same class as guardrail approvals).
func (s *Server) listFencePending(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		http.NotFound(w, r)
		return
	}
	rows := s.Store.AssetsByFence(model.AssetFencePending)
	if rows == nil {
		rows = []model.Asset{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": rows, "count": len(rows)})
}

// resolveFencePending resolves one or more pending assets in one call.
type fencePendingResult struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Fence string `json:"fence,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) resolveFencePending(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		http.NotFound(w, r)
		return
	}
	var in struct {
		IDs     []string `json:"ids"`
		Approve *bool    `json:"approve"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：需要 ids 与 approve"})
		return
	}
	if len(in.IDs) == 0 || in.Approve == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需要 ids（非空）与 approve（true 批准入围栏 / false 拒绝）"})
		return
	}
	s.fenceOutMu.Lock()
	defer s.fenceOutMu.Unlock()
	results := make([]fencePendingResult, 0, len(in.IDs))
	for _, id := range in.IDs {
		asset, ok := s.Store.GetAsset(strings.TrimSpace(id))
		if !ok {
			results = append(results, fencePendingResult{ID: id, Error: "资产不存在"})
			continue
		}
		resolved, err := s.resolvePendingAsset(asset, *in.Approve)
		res := fencePendingResult{ID: asset.ID, Key: asset.Key}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Fence = resolved.Fence
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// changeAssetFence is the membership surface the asset page and the import
// review use: join adds the allow row + in_fence, leave removes it, stops the
// directions that tested the host and marks the asset denied.
func (s *Server) changeAssetFence(w http.ResponseWriter, r *http.Request) {
	if harnessSession(r) {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	var in struct {
		Action string `json:"action"` // join|leave
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON：需要 action（join|leave）"})
		return
	}
	asset, ok := s.Store.GetAsset(strings.TrimSpace(id))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "资产不存在：" + id})
		return
	}
	s.fenceOutMu.Lock()
	defer s.fenceOutMu.Unlock()
	var (
		out model.Asset
		err error
	)
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "join":
		if asset.Fence == model.AssetFencePending {
			// Joining a queued asset IS the approval: skipping the resolve
			// would leave the stop guidance active against an allowed host.
			out, err = s.resolvePendingAsset(asset, true)
		} else {
			out, err = s.joinFence(asset)
		}
	case "leave":
		out, err = s.leaveFence(asset)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "action 只能是 join 或 leave"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	s.recordAction("fence-asset", out.ID, in.Action+"："+out.Key)
	writeJSON(w, http.StatusOK, map[string]any{"asset": out})
}
