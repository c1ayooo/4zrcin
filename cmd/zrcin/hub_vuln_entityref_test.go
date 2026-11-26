package main

import (
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/store"
	"github.com/c1ayooo/4zrcin/internal/vulnlib"
)

// The library outlives the project, so the entry must carry the fact's asset
// identity (entity_ref) at write time: the asset drill-down (spec #1 工单2)
// groups vulnerabilities under assets, and after a delete there is no ledger
// left to ask which host the finding named. A declaration without the field
// stays empty — the console lists it under "未关联资产" rather than guessing
// from the title.
func TestRecordVulnCarriesEntityRefIntoTheLibrary(t *testing.T) {
	lib, err := vulnlib.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := &project.Project{
		Manifest: project.Manifest{ID: "p_1", Name: "proj"},
		CaseDir:  t.TempDir(),
		Store:    st,
	}
	h := &hub{vulns: lib}
	h.recordVuln(p, model.Fact{
		ID: "f_1", Kind: "vuln", Title: "越权读订单", Status: "verified",
		Severity: model.SeverityCritical, EntityRef: "target.example",
	}, nil)
	h.recordVuln(p, model.Fact{
		ID: "f_2", Kind: "vuln", Title: "标题里提到 target.example 但没有声明身份", Status: "verified",
	}, nil)
	entries := lib.List("p_1", "", 10)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	bySource := map[string]vulnlib.Entry{}
	for _, e := range entries {
		bySource[e.SourceID] = e
	}
	if got := bySource["f_1"].EntityRef; got != "target.example" {
		t.Fatalf("declared entity_ref = %q, want target.example", got)
	}
	if got := bySource["f_2"].EntityRef; got != "" {
		t.Fatalf("undeclared entity_ref invented: %q", got)
	}
}
