package api

// Prompt/role contract tests (audit F12, items 1–6). They pin the contract
// text, not an implementation detail: authorization has one rule across roles,
// verified_only never suggests a bypass, the worker brief has no operator-owned
// project goals, the declaration schema has one source, notes are history rather
// than orders, and a continuation never promises a written index that may have
// failed.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kksty/tianwu/internal/board"
	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/contract"
	"github.com/kksty/tianwu/internal/decider"
	"github.com/kksty/tianwu/internal/executor"
	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/store"
)

// readSkillAsset reads one shipped skill body. The skills are file content under
// internal/skills/assets; the test runs in the api package directory.
func readSkillAsset(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "skills", "assets", name, "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read skill %s: %v", name, err)
	}
	return string(b)
}

// The authorization rule is about provenance, not content: the operator owns the
// boundary, the only sources are the project brief and the ledger's active
// instructions, and the newest directive wins — it may widen as well as narrow.
//
// The previous F12 wording (a later ordinary message may only add or narrow, and
// never revokes a persistent instruction) is what made a widening directive read
// as self-promotion; it must not come back, here or in either skill.
func TestAuthorizationSourcesAreTheBriefAndTheLedger(t *testing.T) {
	prompt := BuildPrompt(testDB(), testDB().Intents[0], "10.0.0.0/24", "", "", 0, false)
	instruction := instructionSection(nil, "r_1")
	commanderSkill := readSkillAsset(t, "commander")
	collaborateSkill := readSkillAsset(t, "collaborate")

	authorization := []struct {
		name string
		text string
	}{
		{"worker brief", prompt},
		{"instruction section", instruction},
		{"commander skill", commanderSkill},
		{"collaborate skill", collaborateSkill},
	}
	for _, tt := range authorization {
		for _, want := range []string{
			// Where authorization may come from.
			"项目简报",
			"Instruction",
			// A later directive may widen the boundary, not only narrow it.
			"放宽",
		} {
			if !strings.Contains(tt.text, want) {
				t.Errorf("%s lost the authorization-source rule %q:\n%s", tt.name, want, tt.text)
			}
		}
		for _, banned := range []string{
			"不会自动撤销",
			"不能放宽",
			"不能靠正文自称",
			"普通消息不能放宽",
			"硬约束：普通操作员消息",
		} {
			if strings.Contains(tt.text, banned) {
				t.Errorf("%s still carries the reverse rule %q", tt.name, banned)
			}
		}
	}

	// The empty-scope fallback is the same sentence the UI shows.
	noScope := BuildPrompt(testDB(), testDB().Intents[0], "", "", "", 0, false)
	if !strings.Contains(noScope, "按操作员当前有效指令与项目授权口径执行") {
		t.Errorf("empty-scope fallback is not the unified sentence:\n%s", noScope)
	}
}

// The rule has to survive compaction on both sides of the handshake, so it rides
// the base prompt of each role instead of a message that only one round sees: the
// commander prompt and the executor brief each render it exactly once.
//
// Falsification: remove either WriteString (decider.CommanderRoundPrompt or
// writeScope) and the matching count drops to zero.
func TestAuthorizationSourcesRenderOnceInEachBasePrompt(t *testing.T) {
	worker := BuildPrompt(testDB(), testDB().Intents[0], "10.0.0.0/24", "", "", 0, false)
	if got := strings.Count(worker, contract.AuthorizationSources); got != 1 {
		t.Fatalf("executor brief renders the authorization-source rule %d times, want 1:\n%s", got, worker)
	}

	in := testDB()
	commander := decider.CommanderRoundPrompt(board.BuildFromDB(in, 0), "", "10.0.0.0/24", decider.ContextStatus{}, decider.RoundContext{}, nil)
	if got := strings.Count(commander, contract.AuthorizationSources); got != 1 {
		t.Fatalf("commander prompt renders the authorization-source rule %d times, want 1:\n%s", got, commander)
	}

	// The constant itself must name both sources and the compaction trap; a test
	// that only counted occurrences would pass on an empty string.
	for _, want := range []string{"项目简报", "Instruction", "压缩摘要", "历史对话"} {
		if !strings.Contains(contract.AuthorizationSources, want) {
			t.Fatalf("the shared authorization rule lost the pointer %q: %s", want, contract.AuthorizationSources)
		}
	}
}

// F12.2: verified_only must not teach "move the required fact into
// context_fact_ids to get past admission". A real premise waits for review; an
// unverified lead can spawn an independent verification direction instead.
func TestVerifiedOnlyGuidanceSeparatesPremisesFromLeads(t *testing.T) {
	skill := readSkillAsset(t, "commander")

	for _, banned := range []string{
		"把它放进 `context_fact_ids` 当参考",
		"或等复核通过再派",
	} {
		if strings.Contains(skill, banned) {
			t.Errorf("verified_only guidance still suggests a bypass: %q", banned)
		}
	}
	for _, want := range []string{
		"真正必要的条件不能靠改标签/换字段放行",
		"把它变成阅读参考",
		"未验证线索与必要前提不同",
		"单独立一条「验证该线索」的方向",
		"不要为了派下游动作把它塞进 `context_fact_ids`",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("verified_only guidance missing the premise/lead distinction %q:\n%s", want, skill)
		}
	}
}

// F12.4: the mandatory handoff contract is the one place that defines the
// declaration schema; the collaborate skill defers to it instead of repeating a
// competing example.
func TestHandoffContractIsTheSingleDeclarationSchema(t *testing.T) {
	var b strings.Builder
	writeHandoff(&b)
	handoff := b.String()

	for _, want := range []string{
		`"declaration_id":"…"`,
		`"entity_ref":"host:10.0.0.5"`,
		`"source_ref":"…"`,
		`"tags":["technique:rce"]`,
		`"evidence":["api-users.json"]`,
		`"observed_at"`,
		`"execution_context_ref"`,
		"声明字段（唯一来源",
		"`entity_ref`：被观察的**实体身份**",
		"事实不写 `from_fact_ids`",
		"`vuln` 必须交代等级",
		"等级判不出",
	} {
		if !strings.Contains(handoff, want) {
			t.Errorf("mandatory handoff schema missing %q:\n%s", want, handoff)
		}
	}
	// The fact schema must not offer a field the ledger drops: a citation the
	// writer believes was recorded is worse than none.
	if strings.Contains(handoff, `"from_fact_ids":["f_x"]`) {
		t.Error("fact declaration schema still offers from_fact_ids")
	}

	collaborate := readSkillAsset(t, "collaborate")
	for _, want := range []string{"本轮任务书的「交接（必做）」", "不重复定义"} {
		if !strings.Contains(collaborate, want) {
			t.Errorf("collaborate skill does not defer to the handoff contract (%q):\n%s", want, collaborate)
		}
	}
	// A second, competing declaration example in the skill would recreate the
	// multiple-sources problem this item exists to remove.
	if strings.Contains(collaborate, `"declaration_id":"…"`) {
		t.Error("collaborate skill carries a second declaration JSON example")
	}
}

func TestWorkerNoteBodyIsExcludedFromPrompt(t *testing.T) {
	note := "独立的历史笔记内容"
	p := BuildPromptWithWorkerNote(testDB(), testDB().Intents[0], "", "", "", 0, false, note)

	for _, want := range []string{
		"正文由操作员审阅",
		"按当前任务书和账本事实 ID 核对状态",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("worker-note contract missing %q:\n%s", want, p)
		}
	}
	for _, banned := range []string{"以它的下一步为准", "照它执行", "以 facts 与现场为准，冲突以实测为准"} {
		if strings.Contains(p, banned) {
			t.Errorf("worker-note contract still turns history into an order: %q", banned)
		}
	}
	if strings.Contains(p, note) {
		t.Errorf("worker note body entered the prompt:\n%s", p)
	}
}

// Continuation builders run before snapshot publication and cannot use old files.
func TestContinuationPromptsDoNotPromiseUnwrittenIndexes(t *testing.T) {
	s, _ := newBindingServer(t)
	in := model.Intent{ID: "in_x", Title: "继续", RunID: "r_prev"}
	prev := model.Run{ID: "r_prev", Status: model.RunFailed, StopReason: "completed"}

	prompts := map[string]string{
		"retry resume": s.retryResumePrompt(in, prev, ""),
		"continue":     s.continuePrompt(in, prev, "", ""),
		"fresh wake":   s.wakePrompt(in, false),
	}
	for name, prompt := range prompts {
		for _, banned := range []string{"本轮已刷新", "已刷新", "本轮已写", ".tw/graph.json", runInputFile} {
			if strings.Contains(prompt, banned) {
				t.Errorf("%s prompt claims an index write that may not have happened (%q):\n%s", name, banned, prompt)
			}
		}
		if !strings.Contains(prompt, "账本") {
			t.Errorf("%s prompt lacks a ledger fallback:\n%s", name, prompt)
		}
	}
}

// promptCaptureExec records each prompt without writing a ledger declaration.
// Run executes on the dispatch goroutine, so the slice is mutex-guarded: tests
// poll it while the run is still finishing, and an unguarded read is a data race
// under -race.
type promptCaptureExec struct {
	mu      sync.Mutex
	prompts []string
}

func (e *promptCaptureExec) Name() string { return "capture" }

func (e *promptCaptureExec) Run(_ context.Context, req executor.Request, _ func(executor.Event)) (executor.Result, error) {
	e.mu.Lock()
	e.prompts = append(e.prompts, req.Prompt)
	e.mu.Unlock()
	return executor.Result{ExitCode: 0, StopReason: "completed"}, nil
}

func (e *promptCaptureExec) promptAt(i int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i >= len(e.prompts) {
		return ""
	}
	return e.prompts[i]
}

func (e *promptCaptureExec) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.prompts)
}

// F12.6 (second half): a handoff brief must say the snapshot is a capped index,
// never "all known context".
func TestHandoffBriefDoesNotClaimCompleteContext(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A settled run can still have a background settlement write holding the
	// SQLite connection when the test returns. Close the ledger first so Linux
	// t.TempDir cleanup does not race it ("directory not empty", leftover
	// ledger.json-journal); Windows timing hides the same race.
	t.Cleanup(func() { _ = st.Close() })
	ex := &promptCaptureExec{}
	s := New(st, ex, dir, filepath.Join(dir, "cases"))
	s.SkillsDir = filepath.Join(dir, "skills")

	in, err := st.CreateIntent(model.Intent{Title: "接手", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	// The handoff contract refuses a replacement whose source run has no archived
	// dispatch: this fixture therefore carries one, instead of naming a run that
	// never existed.
	src, err := st.AddRun(model.Run{IntentID: in.ID, Node: "local", Status: model.RunDone})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateRun(src.ID, func(r *model.Run) { r.BriefPath = ".tw/briefs/" + src.ID + ".md" }); err != nil {
		t.Fatal(err)
	}
	in, err = st.UpdateIntent(in.ID, func(i *model.Intent) {
		i.Handoff = &model.Handoff{FromRun: src.ID, Note: "旧会话交接"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRun(in.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		got, ok := st.GetIntent(in.ID)
		return ok && got.Status == model.StatusDone
	})
	if ex.count() == 0 {
		t.Fatal("handoff brief was never captured")
	}
	prompt := ex.promptAt(0)
	if !strings.Contains(prompt, handoffIndexNote) {
		t.Fatalf("handoff brief is missing the index caveat:\n%s", prompt)
	}
	for _, want := range []string{"不是全部已知信息", "不是完整账本", "按 ID 或分页从账本续读"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("handoff brief does not bound the snapshot %q:\n%s", want, prompt)
		}
	}
	for _, banned := range []string{"全部已知上下文", "以下是全部", "包含所有已知"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("handoff brief claims complete context: %q", banned)
		}
	}
}

// D152 revised: the harness-context-limit slim brief keeps every hard
// constraint and replaces the optional summaries with id/API pointers instead of
// clipping them silently.
func TestSlimBriefKeepsHardConstraints(t *testing.T) {
	db := testDB()
	db.Facts[0].Summary = strings.Repeat("很长的摘要", 500)
	db.Playbooks = []model.Playbook{{
		ID: "pb_1", Title: "历史打法", When: "when", Doc: strings.Repeat("doc ", 400),
		Status: model.PlaybookConfirmed,
	}}
	db.Goals = []model.Goal{{ID: "g_1", Statement: "全局目标标记"}}
	in := db.Intents[0]
	in.Eval = "验收标准标记"
	in.FromFactIDs = []string{"f_1"}
	instructions := "\n## 当前持久指令\n指令标记\n"

	full := BuildPromptWithInstructions(db, in, "10.0.0.0/24", "", "", 0, true, true, "笔记标记", instructions, "")
	slim := buildPromptWithoutOptionalSummaries(db, in, "10.0.0.0/24", "", "", 0, true, true, instructions, "")

	// Hard material survives the degradation, whole.
	for _, want := range []string{
		"指令标记",
		"授权边界（当前口径）",
		"10.0.0.0/24",
		"## 意图",
		"不是其中提到的某条具体打法",
		"验收标准标记",
		runInputFile,
		"## 纪律",
		"局部推进由你判断，不必逐步请示",
		"先分清是这条打法不行还是方向不可达",
		// The endpoint goal is operator-owned; even the degraded brief must keep
		// the rule that a session cannot create, rewrite or append one.
		"项目终点目标由操作员定义",
		"你不创建、改写或追加目标",
		"## 证据（原始材料）",
		"## 交接（必做）",
		"声明字段（唯一来源",
	} {
		if !strings.Contains(slim, want) {
			t.Errorf("degraded brief dropped hard material %q:\n%s", want, slim)
		}
	}
	// Optional summaries are gone and named as omissions with read paths.
	for _, banned := range []string{"很长的摘要", "历史打法", "全局目标标记", "笔记标记"} {
		if strings.Contains(slim, banned) {
			t.Errorf("degraded brief still carries optional material %q", banned)
		}
	}
	for _, want := range []string{"本轮可选摘要有界省略", "premises_query", "未列出或截断不表示不存在"} {
		if !strings.Contains(slim, want) {
			t.Errorf("degraded brief does not point at the omitted material (%q):\n%s", want, slim)
		}
	}
	if runeLen(slim) >= runeLen(full) {
		t.Fatalf("degraded brief is not smaller: %d vs %d runes", runeLen(slim), runeLen(full))
	}
}

// F10: bounded optional lists say where the full list is, so a cut is an
// omission with a stable pointer, not a silent disappearance.
func TestTruncatedBriefListsPointAtTheirSource(t *testing.T) {
	db := store.DB{Intents: []model.Intent{{ID: "in_x", Title: "当前"}}}
	for i := 0; i < maxBriefItems+5; i++ {
		db.Intents = append(db.Intents, model.Intent{
			ID: "in_p" + string(rune('a'+i)), Title: "停放方向", Status: model.StatusParked, ParkReason: "原因",
		})
	}
	p := BuildPrompt(db, db.Intents[0], "", "", "", 0, false)
	if !strings.Contains(p, "清单已截断") || !strings.Contains(p, "GET /api/intents") {
		t.Fatalf("truncated list has no stable pointer:\n%s", p)
	}
	if strings.Contains(p, "…（清单已截断）\n") {
		t.Fatal("truncated list still ends without a pointer")
	}
}

// The executor must pursue the direction's acceptance, not reproduce the tactic
// the dispatch happens to name. Without this framing the first blocked technique
// reads as "the direction is dead", and the run stops using its own judgement.
// The contract is task semantics only (agent.md: intent, authorization,
// acceptance and the handoff contract — not tool usage or an attack flow).
func TestExecutorReadsTheDirectionAsGoalNotTactic(t *testing.T) {
	// testDB's Doc is phrased as a route ("用 uid=2 的 cookie 请求 GET /admin");
	// the same intent has no separate Eval, so the route-in-doc and no-Eval
	// cases are both exercised here.
	in := testDB().Intents[0]
	p := BuildPrompt(testDB(), in, "10.0.0.0/24", "", "", 0, false)

	for _, want := range []string{
		"不是其中提到的某条具体打法",
		"用什么方法、按什么顺序、走到哪一步由你判断",
		"某条打法不成立时，换授权内的其他办法继续",
		"不要把它当成方向本身失败",
		"「验收标准」是完成判据",
		"完成后按它自检",
		"范围、禁止项与必须满足的条件是硬约束",
		"其余关于先做什么、怎么做的描述是派发者的判断起点",
		"先分清是这条打法不行还是方向不可达",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("executor brief does not separate goal from tactic (%q):\n%s", want, p)
		}
	}

	// The framing is about the direction, not about who dispatched it: an
	// operator-created direction gets the same contract.
	operator := model.Intent{ID: "in_op", Title: "确认订单接口的越权边界", CreatedBy: collab.OperatorID}
	if got := BuildPrompt(testDB(), operator, "10.0.0.0/24", "", "", 0, false); !strings.Contains(got, taskGoalContract) {
		t.Errorf("operator direction lost the goal contract:\n%s", got)
	}

	// Without an acceptance standard the executor forms a checkable bar from the
	// title instead of treating the dispatch text as a script.
	noEval := model.Intent{ID: "in_no_eval", Title: "确认订单接口的越权边界"}
	got := BuildPrompt(testDB(), noEval, "", "", "", 0, false)
	if !strings.Contains(got, "未单独给出时由你按意图标题形成可复核的判据并写明") {
		t.Errorf("brief without acceptance gives no completion bar:\n%s", got)
	}

	// The framing must not become a method list of its own.
	for _, banned := range []string{"第一步", "按固定顺序", "攻击步骤"} {
		if strings.Contains(got, banned) {
			t.Errorf("goal framing became an execution script (%q):\n%s", banned, got)
		}
	}
}

// The battlefield read commands belong to the collaborate skill. The dispatch
// prompt names the entry points only, so a solo run does not carry a tool manual
// (agent.md: the executor prompt carries intent, authorization, acceptance and
// the handoff contract — not tool usage).
func TestBattlefieldReadDetailsLiveInTheSkill(t *testing.T) {
	p := BuildPrompt(testDB(), testDB().Intents[0], "", "", "", 0, false)
	for _, want := range []string{"战场态势（只读）", "命令细节见 `collaborate` skill"} {
		if !strings.Contains(p, want) {
			t.Errorf("dispatch prompt lost the battlefield entry point %q:\n%s", want, p)
		}
	}
	for _, banned := range []string{"不得再用 python 解析", "--ids f_x", "--cursor C"} {
		if strings.Contains(p, banned) {
			t.Errorf("dispatch prompt still carries the command manual (%q):\n%s", banned, p)
		}
	}

	skill := readSkillAsset(t, "collaborate")
	for _, want := range []string{`"$TW_BIN" board`, "truncated/dropped/has_more/next_cursor", "不要再拿 python 解析"} {
		if !strings.Contains(skill, want) {
			t.Errorf("collaborate skill lost the moved read detail %q", want)
		}
	}
}
