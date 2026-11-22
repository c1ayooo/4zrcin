package bench

import (
	"strings"
	"testing"
	"time"
)

// One goal per challenge: the text names the challenge (so a receipt says which
// one it was about) and the platform's judgement (so it is judgeable), and two
// challenges never produce the same statement.
func TestGoalStatementIsPerChallengeAndJudgeable(t *testing.T) {
	desc := "通过 SQL 注入获取管理员凭证"
	a := GoalStatement(Challenge{UniqueCode: "web_sql_injection_01", Description: &desc})
	if !strings.Contains(a, "web_sql_injection_01") || !strings.Contains(a, desc) ||
		!strings.Contains(a, "被平台判定为 correct") {
		t.Fatalf("statement = %q, want code, description and the platform verdict", a)
	}
	b := GoalStatement(Challenge{UniqueCode: "crypto_rsa_02"})
	if a == b || !strings.Contains(b, "crypto_rsa_02") || strings.Contains(b, "web_sql_injection_01") {
		t.Fatalf("second statement = %q, want it bound to its own challenge", b)
	}
}

// The last write of a run is the one that can point a commander at somebody
// else's container. It must come from the final snapshot only — and when nothing
// is available it must name no address at all, while still saying why.
func TestFinalScopeNeverKeepsHistoricalAddresses(t *testing.T) {
	// The previous document held 10.0.1.5:8080; the last snapshot has nothing
	// available (the challenge closed, and a pending one may still carry a stale
	// address in its list).
	last := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "web_sql_injection_01", ContainerStatus: ContainerStopped},
		{UniqueCode: "pwn_stack_03", ContainerStatus: ContainerPending, ContainerAddr: []string{"10.0.1.9:8080"}},
	})
	text, err := FinalScope(last, "challenges-20261122T083000.000Z-014.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"10.0.1.5:8080", "10.0.1.9:8080"} {
		if strings.Contains(text, stale) {
			t.Fatalf("final scope kept a stale address %s:\n%s", stale, text)
		}
	}
	if !strings.Contains(text, "没有进行中的题") || !strings.Contains(text, "challenges-20261122T083000.000Z-014.json") {
		t.Fatalf("final scope does not state the fact and its evidence:\n%s", text)
	}

	// If something is still up, the final text names exactly that set.
	live := NewSnapshot(time.Now(), []Challenge{
		{UniqueCode: "web_sql_injection_01", ContainerStatus: ContainerAvailable, ContainerAddr: []string{"10.0.2.7:8080"}},
	})
	text, err = FinalScope(live, "challenges-20261122T083100.000Z-015.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "10.0.2.7:8080") || strings.Contains(text, "10.0.1.5:8080") {
		t.Fatalf("final scope does not match the live snapshot:\n%s", text)
	}

	// The evidence file is not optional: this write has to stay checkable.
	if _, err := FinalScope(last, "  "); err == nil {
		t.Fatal("final scope accepted an empty snapshot file name")
	}
}
