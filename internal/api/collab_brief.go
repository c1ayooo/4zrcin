package api

// Collaboration capability inventory for a dispatched executor.
//
// Alignment with sapper matters here: its workers learn that they can message
// the commander and share a fact graph from the mission text, not by guessing.
// 4zrcin already has the mechanisms (participants, child intents, per-run
// identity) but a worker that never reads the collaborate skill cannot know they
// exist. This section lists only *what is available* — no commands, no workflow,
// no role names — so a model that decides it needs a peer or child executor can
// find the contract on its own (docs/01).

import (
	"fmt"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/model"
)

const collabBriefMaxPeers = 6

// collaborationSection always carries the capability inventory: who the peers
// are is conditional, the fact that a peer or a child can be created is not.
//
// A solo direction used to get "" here, and the spawn endpoint — the one entry a
// model cannot guess from the collaborate skill — disappeared with it. That is
// silent discouragement, and it is the shape 90 of 95 first touches had in the
// 2026-11-22 package: nothing forbade delegation, the prompt simply never said
// the door existed. The peer list is skipped when there is nobody to list; the
// actions and the endpoint stay.
func (s *Server) collaborationSection(self model.Intent) string {
	var peers []string
	for _, in := range s.Store.ListIntents() {
		if in.ID == self.ID {
			continue
		}
		switch in.Status {
		case model.StatusDone, model.StatusFailed, model.StatusReviewFailed, model.StatusParked:
			continue // no live holder: a message would be queued forever
		}
		peers = append(peers, fmt.Sprintf("%s（%s，%s）", clipRunes(in.Title, 40), collab.ParticipantID(in.ID), in.Status))
		if len(peers) >= collabBriefMaxPeers {
			break
		}
	}
	var b strings.Builder
	b.WriteString("\n\n## 自主协作（不必经过指挥官）\n")
	b.WriteString("协作是手段不是流程：先看同伴在做什么、账本里有没有能直接用的结论与打法；能独立完成就直接做。需要并行验证、独立证据归属、专门视角，或某个子目标适合另一名执行者时，直接给同伴发消息或在当前意图下派子意图，不必先请示。不同方向的结论要汇回共享账本：关键结论写成事实，可复用的走法写成 playbook，让后面的意图和同伴能直接取用；共享现场/账号冲突先协调。不要为凑层级拆任务，也不要重复别人的意图；是否协作、何时委派由你判断。\n")
	if len(peers) > 0 {
		b.WriteString("- 同伴：" + strings.Join(peers, "；") + "\n")
	}
	b.WriteString("- 可用动作：给同伴/指挥官发消息；在当前意图下派子意图；发布不挂父意图的平级意图（平级、不需等待，发起者只记出处，结果以事实与机械通知回来）；申请新执行者接班。\n")
	// The one executable entry this inventory carries. Every other mechanism in
	// this section is discoverable from the collaborate skill; a child task is
	// not, because a model that never opens the skill cannot guess the endpoint,
	// and 317 runs of 2026-11-22 dispatched zero times for exactly that reason.
	// The URL is the participant mux one ($ZRCIN_AGENT), not the instance root
	// ($ZRCIN_API): the root has no spawn route and a literal root URL 404s.
	// It stays a capability, not a recipe: no workflow, no retry policy, and the
	// rest of the section still carries no commands (collab_brief_test.go).
	b.WriteString("- 派子意图的可执行入口：`POST $ZRCIN_AGENT/spawn`（`idempotency_key` 与 `title`/`description` 必填；挂 `parent_intent_id=$ZRCIN_SELF` 是委托，不挂父意图的平级发布还必须至少引用一条已记录事实：`from_fact_ids`/`context_fact_ids`/`pinned_fact_ids`）；容量满时返回 `started=false` 与原因，用同一把键重试。harness 原生 `subagent` 不登记 run/usage，需要账本归属时用 spawn。\n")
	b.WriteString("- 现场交接：可直接使用意图/插话提供的账号登录；扫码、验证码或人工确认时，可随人工请求关联自己的可见浏览器 session，宿主自动跟随本轮；人工完成后继续。多站点按 URL 选择页面，同站点多账号使用独立浏览器上下文。原生结果可按需查询。\n")
	b.WriteString("- 契约与身份：harness skill `collaborate`（`cyber://skills/collaborate/SKILL.md`），")
	b.WriteString("本轮身份在 `.zrcin/identity.json`。\n")
	return b.String()
}
