package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExplicitCursorRepairRequiresPreservedClaimsAndUnchangedFile(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "in_1")
	st, run := newWarningLedger(t)
	raw := `{"kind":"note","title":"fixture","declaration_id":"fixture-observation","summary":"kept"}` + "\n"
	writeFile(t, filepath.Join(work, HandoffDir, HandoffFile), raw)
	plan, err := PlanCursorRepair(st.Snapshot(), root, work, "in_1")
	if err != nil || len(plan.Problems) == 0 {
		t.Fatalf("missing fact was skippable: %+v %v", plan, err)
	}
	(&Ingestor{Store: st}).OnFinish(run.ID, "in_1", root, work, time.Now().Add(time.Hour))
	if err = CommitDeclared(work, 0); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanCursorRepair(st.Snapshot(), root, work, "in_1")
	if err != nil || len(plan.Problems) > 0 || plan.Matched != 1 {
		t.Fatalf("preserved claim did not match: %+v %v", plan, err)
	}
	before := st.Snapshot().Facts[0]
	if err = CommitCursorRepair(work, plan); err != nil || Consumed(work) == 0 {
		t.Fatalf("commit: %v", err)
	}
	after := st.Snapshot().Facts[0]
	if before.ClaimVersion != after.ClaimVersion {
		t.Fatal("cursor repair rewrote a claim")
	}
	if err = CommitDeclared(work, 0); err != nil {
		t.Fatal(err)
	}
	plan, _ = PlanCursorRepair(st.Snapshot(), root, work, "in_1")
	if err = os.WriteFile(filepath.Join(work, HandoffDir, HandoffFile), []byte(raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = CommitCursorRepair(work, plan); err == nil {
		t.Fatal("changed handoff file was consumed")
	}
}

func TestCursorRepairCannotSkipAnUnpreservedPlaybook(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "in_1")
	st, run := newWarningLedger(t)
	raw := `{"kind":"playbook","title":"fixture tactic","summary":"applicable","doc":"kept content","from_fact_ids":[]}` + "\n"
	writeFile(t, filepath.Join(work, HandoffDir, HandoffFile), raw)
	plan, err := PlanCursorRepair(st.Snapshot(), root, work, "in_1")
	if err != nil || len(plan.Problems) == 0 {
		t.Fatalf("uncommitted playbook was skippable: %+v %v", plan, err)
	}
	(&Ingestor{Store: st}).OnFinish(run.ID, "in_1", root, work, time.Now().Add(time.Hour))
	if err = CommitDeclared(work, 0); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanCursorRepair(st.Snapshot(), root, work, "in_1")
	if err != nil || len(plan.Problems) != 0 {
		t.Fatalf("preserved playbook rejected: %+v %v", plan, err)
	}
}
