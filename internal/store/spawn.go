package store

// Spawn is the ledger half of "create one more execution unit".
//
// A spawned worker is still an intent (it is a direction of work with its own
// case, claim and run); the difference from a child intent is that it may have
// no parent direction at all. The creation and its idempotency check happen
// under the store lock, so two concurrent spawns with the same key cannot both
// create a worker: one wins and the other observes the existing row.
//
// Two ways to name the source direction exist here:
//   - ParentID: a delegation under a direction (the wait axis);
//   - sourceIntentID: the caller's own direction when it publishes a *peer*
//     direction (no ParentID). The API passes the participant identity's intent,
//     so a peer publish inherits the caller's operator constraints, skills and
//     node without inventing a delegation link. Passing "" for both means a
//     commander/operator spawn, which keeps the old behaviour.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/model"
)

var (
	ErrSpawnInvalid             = errors.New("造人请求不合法")
	ErrSpawnIdempotencyConflict = errors.New("同一个幂等键对应不同的 worker")
)

// CreateSpawnIntent records one spawned worker. When the request repeats an
// idempotency key with the same payload, the original intent is returned with
// created=false instead of a second worker being made.
//
// A spawn beneath parentID inherits the operator constraints (Doc), the skills
// and the node default from that direction, exactly like a child intent; it does
// not inherit its model profile (an empty profile goes through the normal
// shared gateway connection). sourceIntentID does the same for a peer publish
// (ParentID empty): it names the publishing executor's own direction, and it is
// also the node lock: a peer publish must stay on the node its source direction
// belongs to, so "publish more work" cannot silently become "relocate it".
func (s *Store) CreateSpawnIntent(in model.Intent, sourceIntentID string) (model.Intent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	requestedNode := in.Node
	sourceID := sourceIntentID
	if sourceID == "" {
		sourceID = in.ParentID
	}
	if in.ParentID != "" {
		parent, ok := s.intentLocked(in.ParentID)
		if !ok {
			return model.Intent{}, false, fmt.Errorf("父意图 %s 不存在", in.ParentID)
		}
		switch parent.Status {
		case model.StatusPending, model.StatusClaimed, model.StatusRunning:
		default:
			return model.Intent{}, false, fmt.Errorf("父意图 %s 是 %s：不能再造人", in.ParentID, parent.Status)
		}
	}
	var source *model.Intent
	if sourceID != "" {
		cur, ok := s.intentLocked(sourceID)
		if !ok {
			return model.Intent{}, false, fmt.Errorf("%w：来源意图 %s 不存在", ErrSpawnInvalid, sourceID)
		}
		source = cur
	}
	if source != nil {
		// The node lock only applies to a peer publish (ParentID empty): a
		// delegated child keeps the old "explicit node wins, else inherit"
		// rule because the delegating direction already constrained it.
		if in.ParentID == "" && collab.RoleOf(in.CreatedBy) == collab.RoleExecutor &&
			requestedNode != "" && requestedNode != source.Node {
			return model.Intent{}, false, fmt.Errorf("%w：你所在意图在节点 %s，这次请求指定节点 %s；平级发布只能留在同一节点，不填 node 会继承所在意图。",
				ErrPeerNodeLocked, nodeLabel(source.Node), nodeLabel(requestedNode))
		}
		in.Doc = source.Doc // operator constraints are inherited, not authored by the publisher
		in.Skills = unionSkills(source.Skills, in.Skills)
		if in.Node == "" {
			in.Node = source.Node
		}
	}
	if in.IdempotencyKey != "" {
		for _, cur := range s.db.Intents {
			if cur.IdempotencyKey != in.IdempotencyKey || cur.CreatedBy != in.CreatedBy ||
				cur.ParentID != in.ParentID {
				continue
			}
			// Skill and node comparisons use the post-inheritance values: a
			// retry that lists only its own additions must match the union the
			// first request stored, otherwise the same key would look like a
			// different payload. Node is still only compared when the request
			// named one, because dispatch pins the stored node to where the
			// worker actually ran.
			if cur.Title != in.Title || cur.Description != in.Description ||
				requestedNode != "" && cur.Node != in.Node ||
				cur.Eval != in.Eval || cur.Profile != in.Profile ||
				cur.MaxTurns != in.MaxTurns ||
				!slices.Equal(cur.Skills, in.Skills) ||
				!slices.Equal(cur.FromFactIDs, in.FromFactIDs) ||
				!slices.Equal(cur.ContextFactIDs, normalizeContextIDs(in.ContextFactIDs)) ||
				!slices.Equal(cur.PinnedFactIDs, normalizeContextIDs(in.PinnedFactIDs)) {
				return model.Intent{}, false, ErrSpawnIdempotencyConflict
			}
			return detachIntent(cur), false, nil
		}
	}
	if err := s.applyInitialContextLocked(&in); err != nil {
		return model.Intent{}, false, err
	}
	s.snapshotPremiseClaimsLocked(&in)
	if err := s.checkPublishBudgetLocked(in); err != nil {
		return model.Intent{}, false, err
	}
	now := time.Now()
	in.ID = s.uniqueIDLocked("in")
	in.Status = model.StatusPending
	in.Hold = false
	in.Aborted = false
	in.Wait = nil
	in.CreatedAt, in.UpdatedAt = now, now
	in = detachIntent(in)
	s.db.Intents = append(s.db.Intents, in)
	if err := s.flush("intent"); err != nil {
		return model.Intent{}, false, err
	}
	return detachIntent(in), true, nil
}

// unionSkills merges inherited skills with the request's own list, keeping the
// inherited order first and dropping duplicates: a publisher may append skills
// but cannot drop the constraints it inherited.
func unionSkills(inherited, requested []string) []string {
	out := make([]string, 0, len(inherited)+len(requested))
	seen := make(map[string]bool, len(inherited)+len(requested))
	for _, group := range [][]string{inherited, requested} {
		for _, name := range group {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// nodeLabel keeps the refusal sentence readable when a direction has no node
// (the registry default). It is message text only, never a stored value.
func nodeLabel(node string) string {
	node = strings.TrimSpace(node)
	if node == "" {
		return "（默认节点）"
	}
	return node
}
