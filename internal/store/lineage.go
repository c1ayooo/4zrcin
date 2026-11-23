package store

// Publish lineage budgets.
//
// A direction may be created for another reason than "this direction delegated
// work to me": today an executor can publish a *peer* direction (no ParentID)
// that references recorded facts, inherits the same operator constraints and
// works beside it. That is still one provenance tree, and the tree is what the
// budgets below bound:
//
//   - the CreatedBy axis (CreatedBy names the participant that stamped the row,
//     i.e. "pt_in_<intent>" for an executor) is the lineage axis. ParentID is
//     the delegation/wait axis and is deliberately NOT used here: a peer
//     publish has no ParentID but still belongs to the lineage of the direction
//     whose executor published it.
//   - a root is the nearest direction in that chain created by a non-executor
//     (commander/operator/legacy/unknown). A commander-created direction is a
//     root at depth 0; a peer publish by its executor is depth 1, a publish by
//     that peer is depth 2, and so on.
//
// The three gates exist so a run cannot turn "explore a bit more" into an
// unbounded fan-out, and so a lineage cannot grow deeper than a reviewer can
// still follow. They are admission rules, not strategy: the text tells the
// caller which gate refused and what to do instead (merge/close a direction or
// hand the judgement back), it never decides which direction is "better".
//
// Everything here runs under s.mu; the helpers document that with the Locked
// suffix. Creation paths call checkPublishBudgetLocked only for a request that
// actually creates a row (after the idempotent hit): a retry of an already
// recorded publish must return the original row even when the budget has been
// used up in the meantime.

import (
	"errors"
	"fmt"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
)

const (
	// maxPublishPerRun bounds how many directions one executor run may create
	// (peer publishes and child delegations both count: both are "this run
	// added a direction to the ledger").
	maxPublishPerRun = 8
	// maxLineageIntents bounds one lineage tree (root included) so a single
	// direction family cannot take over the project graph.
	maxLineageIntents = 32
	// maxLineageDepth bounds the executor-created hops below the root direction:
	// root itself is 0, the root's executor publishing a peer is 1.
	maxLineageDepth = 3
)

var (
	// ErrLineageBudget means the publish would exceed a lineage gate. The
	// message names the gate, the current usage, the cap and the next action;
	// changing the idempotency key cannot help because the gate counts rows,
	// not requests.
	ErrLineageBudget = errors.New("谱系发布预算已用尽")
	// ErrPeerNodeLocked means an executor tried to publish a peer direction onto
	// a different node than the one its own direction belongs to. A peer
	// publish inherits the execution position; it is not a way to relocate work.
	ErrPeerNodeLocked = errors.New("平级发布不能切换节点")
)

// lineageRootLocked walks the CreatedBy chain from in up to the nearest
// direction created by a non-executor and returns that direction plus the
// number of executor hops from in to it. Caller holds s.mu.
//
// A dangling CreatedBy (the creator direction is not in this ledger: imported
// or legacy rows) or a loop stops the walk at the highest direction that could
// be resolved; the function never hangs and never invents a missing row. A
// non-executor CreatedBy (commander/operator/empty) returns in itself at depth
// 0, which is the "commander direction" case.
func (s *Store) lineageRootLocked(in model.Intent) (model.Intent, int) {
	root := in
	depth := 0
	seen := map[string]bool{}
	if in.ID != "" {
		seen[in.ID] = true
	}
	for hops := 0; hops <= maxLineageIntents; hops++ {
		creatorID, isExecutor := collab.IntentIDFromParticipant(root.CreatedBy)
		if !isExecutor || seen[creatorID] {
			break
		}
		seen[creatorID] = true
		creator, ok := s.intentLocked(creatorID)
		if !ok {
			break
		}
		root = *creator
		depth++
	}
	return root, depth
}

// lineageOfLocked returns the lineage identity of in: the root direction, the
// executor-created depth of in below it (commander direction = 0), and the
// current size of the whole lineage (root included).
//
// in does not have to be persisted yet: a creation path asks "what would the
// lineage look like if this row were added", so an unrecorded in is counted as
// one more member. Caller holds s.mu.
func (s *Store) lineageOfLocked(in model.Intent) (rootID string, depth int, size int) {
	root, depth := s.lineageRootLocked(in)
	rootID = root.ID
	stored := false
	for _, cur := range s.db.Intents {
		if in.ID != "" && cur.ID == in.ID {
			stored = true
		}
		curRoot, _ := s.lineageRootLocked(cur)
		if curRoot.ID == rootID {
			size++
		}
	}
	if !stored {
		size++
	}
	return rootID, depth, size
}

// checkPublishBudgetLocked is the one admission gate for creating a direction on
// behalf of an executor. It is called by CreateSpawnIntent and
// CreateChildIntent after the idempotent lookup and before the row is written.
// Commander/operator-created directions are not budgeted: the commander is the
// one who decides how wide the project's frontier should be.
//
// Caller holds s.mu.
func (s *Store) checkPublishBudgetLocked(in model.Intent) error {
	if collab.RoleOf(in.CreatedBy) != collab.RoleExecutor {
		return nil
	}
	// Gate 1: how many directions this run already created. A run without a
	// recorded identity (empty DelegatedByRun: legacy/imported or a direct store
	// call) cannot be attributed, so only the lineage gates apply.
	if in.DelegatedByRun != "" {
		used := 0
		for _, cur := range s.db.Intents {
			if cur.DelegatedByRun == in.DelegatedByRun {
				used++
			}
		}
		if used >= maxPublishPerRun {
			return lineageBudgetError("本轮发布", used, maxPublishPerRun,
				"本轮先合并或收口已有意图，把需要更多意图的理由交回指挥官，下一轮再发布")
		}
	}
	// Gate 2 and 3: the lineage this direction belongs to. size includes the
	// new row (lineageOfLocked counts an unpersisted in), so "> cap" means
	// "adding it would exceed the cap".
	_, depth, size := s.lineageOfLocked(in)
	if size > maxLineageIntents {
		return lineageBudgetError("谱系总量", size, maxLineageIntents,
			"把剩余判断合并进已有意图，或把判断交回指挥官决定是否另开谱系")
	}
	if depth > maxLineageDepth {
		return lineageBudgetError("谱系深度", depth, maxLineageDepth,
			"把当前结论作为事实或消息交回上级意图，不要再向下发布")
	}
	return nil
}

func lineageBudgetError(gate string, used, limit int, action string) error {
	return fmt.Errorf("%w：闸门=%s；当前用量=%d；上限=%d；该怎么做：%s。不要换 idempotency_key 重试：预算按已记录的行统计，新键不会绕过。",
		ErrLineageBudget, gate, used, limit, action)
}
