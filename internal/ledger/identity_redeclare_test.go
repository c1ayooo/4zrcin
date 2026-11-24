package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

// 反方反例（清扫第 1 轮）：同一 intent 两轮各写一行完全相同的 note。
// 身份现在是行文本摘要，两行落到同一身份；要回答的是「第二次声明会不会把
// 断言洗成新版本」——版本一变，已核验的判定就作废。
func TestIdenticalRedeclarationAcrossRunsKeepsTheAssertion(t *testing.T) {
	casesRoot := t.TempDir()
	dir := filepath.Join(casesRoot, "in_1")
	path := filepath.Join(dir, HandoffDir, HandoffFile)
	line := `{"kind":"note","title":"同一行","summary":"同一句"}`
	writeFile(t, path, line+"\n")

	st := openApplyLedger(t)
	runA := applyRun(t, st, 0)

	first, err := ReadDeclaredSince(casesRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Facts) != 1 {
		t.Fatalf("first read = %+v", first.Facts)
	}
	if err := CommitDeclared(dir, first.Consumed); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyDeclaration(st, "in_1", runA.ID, first.Facts[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := st.FactByID(applied.FactID)
	if !ok {
		t.Fatal("first declaration was not stored")
	}

	// The next round appends a byte-identical line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	runB := applyRun(t, st, first.Consumed)
	second, err := ReadDeclaredSince(casesRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Facts) != 1 {
		t.Fatalf("second read = %+v", second.Facts)
	}
	if _, err := ApplyDeclaration(st, "in_1", runB.ID, second.Facts[0], nil); err != nil {
		t.Fatal(err)
	}

	after, ok := st.FactByID(applied.FactID)
	if !ok {
		t.Fatal("the fact disappeared")
	}
	facts := st.Snapshot().Facts
	t.Logf("facts=%d version %d→%d hash %s→%s status %q→%q source_run %q→%q decl_ref %q→%q",
		len(facts), before.ClaimVersion, after.ClaimVersion,
		before.ClaimHash[:8], after.ClaimHash[:8], before.Status, after.Status,
		before.SourceRunID, after.SourceRunID, before.DeclarationRef, after.DeclarationRef)

	if len(facts) != 1 {
		t.Fatalf("facts = %d, want the one identical claim", len(facts))
	}
	if after.ClaimVersion != before.ClaimVersion || after.ClaimHash != before.ClaimHash {
		t.Fatalf("an identical re-declaration started a new assertion version: v%d %s → v%d %s",
			before.ClaimVersion, before.ClaimHash[:8], after.ClaimVersion, after.ClaimHash[:8])
	}
	if after.Status != before.Status {
		t.Fatalf("an identical re-declaration changed the verdict: %q → %q", before.Status, after.Status)
	}

	// Positive control: a line whose claim really changed — same identity, new
	// sentence — starts a new version on the same row, so the check above is not
	// vacuous.
	ctlA := `{"kind":"note","title":"对照","summary":"第一句","declaration_id":"ctl-1"}`
	writeFile(t, path, ctlA+"\n")
	dA, err := ReadDeclared(casesRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	appliedA, err := ApplyDeclaration(st, "in_1", runB.ID, dA.Facts[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	vA, ok := st.FactByID(appliedA.FactID)
	if !ok {
		t.Fatal("control fact was not stored")
	}

	ctlB := `{"kind":"note","title":"对照","summary":"第二句","declaration_id":"ctl-1"}`
	writeFile(t, path, ctlB+"\n")
	dB, err := ReadDeclared(casesRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	appliedB, err := ApplyDeclaration(st, "in_1", runB.ID, dB.Facts[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	vB, ok := st.FactByID(appliedB.FactID)
	if !ok || appliedB.FactID != appliedA.FactID {
		t.Fatalf("the correction started a second row: %q → %q", appliedA.FactID, appliedB.FactID)
	}
	if vB.ClaimVersion != vA.ClaimVersion+1 || vB.ClaimHash == vA.ClaimHash {
		t.Fatalf("a changed claim did not start a new version: v%d %s → v%d %s",
			vA.ClaimVersion, vA.ClaimHash[:8], vB.ClaimVersion, vB.ClaimHash[:8])
	}
}
