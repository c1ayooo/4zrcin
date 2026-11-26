package api

// The fence-out pending flow (spec #1 工单5). The node refuses an out-of-range
// request and marks the verdict default_missed (fork patch 30); the control
// plane reads that from the capture metadata and makes the ASSET — not the
// request — the operator's decision:
//
//	pending:  the asset is recorded 围栏外 and nothing else moves. No wake, no
//	          marker intent, no standing instruction: the fence already blocked
//	          the traffic, the refusal body tells the executor why, and the
//	          operator finds the queue on the communication page's pending card
//	          (the board's fence block shows it to the commander's next round
//	          for free)
//	approve:  join the fence (allow row + asset in_fence), queue a "continue
//	          testing" intent, tell the commander — the one model round this
//	          flow ever spends
//	reject:   asset denied; the board's fence block carries the standing
//
// The approval lives here, not on the request: a request approval could not
// admit the asset, and the node already refused it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/board"
	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// fenceMessageKind marks the approval message so the console badges it in the
// conversation thread.
const fenceMessageKind = "asset_fence"

// fenceNotifyCtx bounds the best-effort commander delivery an approval makes.
// The message row is the durable record before any delivery runs; a slow or
// failing injection must not hold the handler.
func fenceNotifyCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

// notifyCommander queues one fence lifecycle message to the commander and,
// when the project is live, delivers it into the running round
// (deliverToCommander also wakes the decider; a paused project keeps the row
// queued without a wake). A failure is logged, never fatal: the queued row is
// already the record.
func (s *Server) notifyCommander(ctx context.Context, from, text, idem string) {
	msg, _, err := s.Store.AddMessage(model.Message{
		ProjectID:         s.projectID(),
		FromParticipantID: from,
		ToParticipantID:   collab.CommanderID,
		Kind:              fenceMessageKind,
		Text:              text,
		Durable:           true,
		IdempotencyKey:    idem,
		Status:            model.MessageQueued,
	})
	if err != nil {
		log.Printf("fence 通知指挥官失败：%v", err)
		return
	}
	_ = s.deliverToCommander(ctx, msg)
}

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
// records the pending row and nothing else moves — no wake, no marker intent,
// no standing instruction. The fence already blocked the traffic, the refusal
// body tells the executor why, the board's fence block shows the commander the
// standing, and the operator finds the queue on the communication page's
// pending card. The caller serialises via fenceOutMu, so the check-then-create
// cannot race itself.
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
	return nil
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

// denyAsset is the shared rejection tail (leave and pending-reject): the row
// is the record. The board's fence block shows the standing to every reader;
// no standing instruction is written on the fence's authority — the operator
// can add one from the asset page if they want the words in the rounds.
func (s *Server) denyAsset(asset model.Asset) (model.Asset, error) {
	return s.Store.UpdateAsset(asset.ID, func(a *model.Asset) {
		a.Fence = model.AssetFenceDenied
		a.Source = model.AssetSourceObserved
	})
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

// admitAsset is the shared approval tail: the allow row lands (joinFence), the
// "continue testing" intent is queued, and the commander hears the approval —
// deliverToCommander injects into a live round or queues for the next one.
// This is the one model round the pending flow ever spends. A join that is a
// no-op (already in_fence) stops after joinFence, so a double click or a
// repeat import adds no second intent or message.
func (s *Server) admitAsset(ctx context.Context, asset model.Asset) (model.Asset, error) {
	joined, err := s.joinFence(asset)
	if err != nil {
		return asset, err
	}
	if asset.Fence == model.AssetFenceIn {
		return joined, nil
	}
	continueID := ""
	if in, err := s.Store.CreateIntent(model.Intent{
		Title:       "继续测试 " + asset.Key + "：已加入围栏",
		Description: "操作员批准范围外资产加入围栏，恢复对该主机的测试。",
		CreatedBy:   collab.OperatorID,
	}); err != nil {
		log.Printf("asset %s: 继续测试意图创建失败：%v", asset.Key, err)
	} else {
		continueID = in.ID
	}
	if continueID != "" {
		notifyCtx, cancel := fenceNotifyCtx()
		defer cancel()
		s.notifyCommander(notifyCtx, collab.OperatorID,
			fmt.Sprintf("已批准资产 %s 加入围栏：放行已生效，继续测试意图 %s 已建立，可恢复对该主机的测试。", asset.Key, continueID),
			"fence-admit-msg:"+continueID)
	} else {
		notifyCtx, cancel := fenceNotifyCtx()
		defer cancel()
		s.notifyCommander(notifyCtx, collab.OperatorID,
			fmt.Sprintf("已批准资产 %s 加入围栏：放行已生效，可恢复对该主机的测试（继续测试意图创建失败，见服务日志）。", asset.Key),
			"fence-admit-msg:"+asset.ID)
	}
	return joined, nil
}

// resolvePendingAsset applies one decision. approve joins the fence through
// the shared approval tail (the announcement rides there); reject marks the
// asset denied and stops at the row — the board's fence block carries the
// standing to every reader without spending a model round on it.
func (s *Server) resolvePendingAsset(ctx context.Context, asset model.Asset, approve bool) (model.Asset, error) {
	if asset.Fence != model.AssetFencePending {
		return asset, errors.New("该资产不在待审状态（可能已被处理）")
	}
	if approve {
		return s.admitAsset(ctx, asset)
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
	decision := "reject："
	if *in.Approve {
		decision = "approve："
	}
	results := make([]fencePendingResult, 0, len(in.IDs))
	for _, id := range in.IDs {
		asset, ok := s.Store.GetAsset(strings.TrimSpace(id))
		if !ok {
			results = append(results, fencePendingResult{ID: id, Error: "资产不存在"})
			continue
		}
		resolved, err := s.resolvePendingAsset(r.Context(), asset, *in.Approve)
		res := fencePendingResult{ID: asset.ID, Key: asset.Key}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Fence = resolved.Fence
			s.recordAction("fence-asset", asset.ID, decision+asset.Key)
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
		// Joining is the approval, whatever the row's current standing: a
		// pending asset resolves, a denied one is re-admitted — both through
		// the same tail (guidance revoked, continue intent, commander told).
		out, err = s.admitAsset(r.Context(), asset)
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

// boardFenceBlock assembles the board's fence standing from the two sources
// the fence already has: the manifest's rule table (mode + host rows) and the
// ledger's asset table (pending / denied). Lists are capped — beyond them the
// counts carry the truth and the full table stays on the asset page.
func (s *Server) boardFenceBlock(db store.DB) *board.FenceState {
	out := &board.FenceState{}
	rules, hasRules := s.Project.ScopeRulesOf()
	if hasRules {
		out.Enabled = rules.Enabled
		out.DefaultAction = rules.DefaultAction
		out.Mode = project.FenceModeOf(&rules)
		for _, r := range rules.Rules {
			if r.Host == "*" {
				continue
			}
			if len(out.InRange) < board.MaxFenceInRange {
				out.InRange = append(out.InRange, r.Host)
			}
		}
	} else {
		out.Mode = project.FenceUnfenced
	}
	for _, a := range db.Assets {
		switch a.Fence {
		case model.AssetFencePending:
			out.PendingCount++
			if len(out.Pending) < board.MaxFencePendingList {
				out.Pending = append(out.Pending, a.Key)
			}
		case model.AssetFenceDenied:
			out.DeniedCount++
			if len(out.Denied) < board.MaxFenceDeniedList {
				out.Denied = append(out.Denied, a.Key)
			}
		}
	}
	return out
}
