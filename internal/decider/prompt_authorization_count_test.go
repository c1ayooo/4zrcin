package decider

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
	"github.com/kksty/tianwu/internal/state"
)

// authorizationLines returns the prompt lines that contain the word 授权.
func authorizationLines(prompt string) []string {
	var out []string
	for _, ln := range strings.Split(prompt, "\n") {
		if strings.Contains(ln, "授权") {
			out = append(out, strings.TrimSpace(ln))
		}
	}
	return out
}

// The commander round may only carry the word 授权 from the project documents.
// A retired-phrase list never keeps up with rewording (the eight strings left in
// runner.go are exactly that failure), so the durable form is a count: a rendered
// round carries the word as many times as the injected documents do, not once
// more.
//
// This drives the real Runner rather than CommanderRoundPrompt, because the
// offending strings sit in the round lead and the idle-replan section that Run()
// concatenates around the pure builder. The fixture therefore opens a goal and
// leaves a generic operator request pending with nothing in flight, and runs two
// rounds so the idle and the handoff variants both render.
func TestCommanderRoundAuthorizationCountMatchesTheDocuments(t *testing.T) {
	cases := []struct {
		name string
		docs string
	}{
		{
			name: "documents do not use the word",
			docs: "## 约束\n不要删除或修改真实数据。\n\n## 起点\n10.0.0.0/24\n\n## 目标\n拿到管理员权限\n\n",
		},
		{
			name: "documents use it three times",
			docs: "## 约束\n不要删除或修改真实数据；授权不含 10.0.0.5。\n\n## 起点\n授权范围：10.0.0.0/24\n\n## 目标\n在授权范围内拿到管理员权限\n\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			if _, err := st.AddGoal(model.Goal{Statement: "拿到管理员权限"}); err != nil {
				t.Fatalf("open goal: %v", err)
			}
			// The idle section renders only when a direction exists and every one
			// of them is settled: an open goal alone is not an idle project.
			done, err := st.CreateIntent(model.Intent{Title: "写在前面的一条已完成方向"})
			if err != nil {
				t.Fatalf("create intent: %v", err)
			}
			if _, err := st.UpdateIntent(done.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
				t.Fatalf("settle intent: %v", err)
			}
			// A pending generic request is what makes the idle section print its
			// "fill in the detail" line, the third place the word rides the prompt.
			if _, _, err := st.EnsureIdleOperatorRequest(); err != nil {
				t.Fatalf("idle operator request: %v", err)
			}

			mem := state.New(filepath.Join(t.TempDir(), "state.md"))
			var prompts []string
			r := NewRunner(st, mem, "")
			r.ModelName = "test-model"
			// 110000/128000 puts the second round over the handoff line, so both
			// the plain idle section and the handoff variant render.
			r.ContextWindow = 128000
			r.Scope = tc.docs
			r.Commander = func(_ context.Context, round string, _ bool) (RoundResult, error) {
				prompts = append(prompts, round)
				if len(prompts) == 1 {
					return RoundResult{Tokens: 1, ContextTokens: 110000}, nil
				}
				return RoundResult{Tokens: 1, ContextTokens: 110000}, nil
			}
			r.Run(context.Background(), "first", true)
			r.Run(context.Background(), "second", true)
			if len(prompts) < 2 {
				t.Fatalf("expected two rounds, got %d", len(prompts))
			}

			want := strings.Count(tc.docs, "授权")
			for i, prompt := range prompts {
				// Fixture precondition: the second round must render BOTH the
				// handoff variant and the idle-replan section, because the
				// handoff wording lives in the idle section's handoff branch. If
				// either is missing this test would go green with that branch
				// unfixed, which is the hole it exists to close.
				if i == 1 {
					for _, want := range []string{"## 交接（本轮必做）", "目标未达成，没有在办方向"} {
						if !strings.Contains(prompt, want) {
							t.Fatalf("round 2 does not render %q, so that branch is not covered:\n%s", want, prompt)
						}
					}
				}
				// The documents must arrive whole, or the count would be measuring
				// a prompt that quietly dropped the only legitimate source.
				if strings.Count(prompt, tc.docs) != 1 {
					t.Fatalf("round %d does not carry the documents verbatim exactly once:\n%s", i+1, prompt)
				}
				got := strings.Count(prompt, "授权")
				if got != want {
					t.Fatalf("round %d carries 授权 %d times, want %d (documents only); offending lines:\n%s",
						i+1, got, want, strings.Join(authorizationLines(prompt), "\n"))
				}
			}
		})
	}
}
