// zrcin-repair repairs cursors or derived confidence labels offline. Run with
// the control plane stopped; dry-run is default.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/ledger"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/store"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is separate from main so the flag checks, the pause gate and the
// dry-run/commit boundary can be tested without a real project or stdout.
func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("zrcin-repair", flag.ContinueOnError)
	dir := fs.String("project", "", "暂停项目的绝对目录（先停止 zrcin）")
	intent := fs.String("intent", "", "精确的意图 ID")
	confidence := fs.Bool("confidence", false, "校对全项目的有效置信度")
	rebuildManifest := fs.Bool("manifest", false, "以账本文档为准重建 project.json 的 scope/description")
	commit := fs.Bool("commit", false, "执行已校对的修复；默认只预览")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Manifest rebuild is its own mode: it rewrites project.json from the
	// ledger's document rows (the audited truth) and never touches the ledger
	// or the case tree.
	if *rebuildManifest {
		if !filepath.IsAbs(*dir) || *intent != "" || *confidence {
			return fmt.Errorf("--manifest 需要 --project 的绝对目录，并且不能与 --intent/--confidence 同用")
		}
		return repairManifest(*dir, *commit, stdout)
	}
	// The directory must be absolute because the fix edits it while zrcin is
	// stopped; cursor repair takes a single direction directory name.
	if *confidence {
		if !filepath.IsAbs(*dir) || *intent != "" {
			return fmt.Errorf("--confidence 需要 --project 的绝对目录，并且不能与 --intent 同用")
		}
	} else if !filepath.IsAbs(*dir) || !filepath.IsLocal(*intent) || *intent == "." || filepath.Base(*intent) != *intent || strings.ContainsAny(*intent, "/\\") {
		return fmt.Errorf("必须提供 --project 和精确的 --intent")
	}
	raw, err := os.ReadFile(filepath.Join(*dir, project.ManifestFile))
	if err != nil {
		return err
	}
	var manifest project.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if !manifest.Paused {
		return fmt.Errorf("项目必须暂停，且修复时 zrcin 必须已停止")
	}
	ledgerPath := filepath.Join(*dir, "ledger.sqlite")
	db, err := store.ReadSnapshot(ledgerPath)
	if err != nil {
		return err
	}
	if *confidence {
		plan := store.PlanConfidenceRepair(db)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil || !*commit {
			return err
		}
		if len(plan.Blocked) > 0 {
			return fmt.Errorf("有 %d 条事实需单独核对断言，置信度未修复", len(plan.Blocked))
		}
		if len(plan.Changes) == 0 {
			return nil
		}
		st, err := store.OpenSQLite(ledgerPath)
		if err != nil {
			return err
		}
		defer st.Close()
		backup := filepath.Join(*dir, "ledger-before-confidence-"+time.Now().Format("20060102-150405.000000000")+".sqlite")
		if err := st.BackupTo(backup); err != nil {
			return err
		}
		if err := st.RepairConfidence(plan.Version); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "置信度已重算。修复前备份", backup)
		return nil
	}
	known := false
	for _, in := range db.Intents {
		if in.ID == *intent {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("项目中没有意图 %s", *intent)
	}
	work := filepath.Join(*dir, "cases", *intent)
	plan, err := ledger.PlanCursorRepair(db, filepath.Join(*dir, "cases"), work, *intent)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(plan); err != nil {
		return err
	}
	if !*commit {
		return nil
	}
	if plan.Through <= plan.Before {
		return fmt.Errorf("没有待修复游标")
	}
	if len(plan.Problems) > 0 {
		return fmt.Errorf("存在未核对的断言，未修改游标")
	}
	stamp := time.Now().Format("20060102-150405.000000000")
	record := filepath.Join(work, ledger.HandoffDir, "cursor-repair-"+stamp+".json")
	backup, _ := json.MarshalIndent(map[string]any{"plan": plan, "project": manifest.ID, "intent": *intent, "at": time.Now()}, "", "  ")
	if err = os.WriteFile(record, backup, 0600); err != nil {
		return err
	}
	if err = ledger.CommitCursorRepair(work, plan); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "游标已修复；回滚时按审计文件中的 before 恢复 facts.offset。账本未改写。")
	return nil
}

// repairManifest rebuilds the manifest's scope/description from the ledger's
// document rows. The ledger is the audited truth for document text; the
// manifest is the projection briefs read, so after a failed save rollback the
// two can diverge and this is the explicit, previewable way to converge them.
// Dry-run is the default: without --commit it only prints the planned text.
func repairManifest(dir string, commit bool, stdout io.Writer) error {
	path := filepath.Join(dir, project.ManifestFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest project.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("解析 %s: %w", path, err)
	}
	if !manifest.Paused {
		return fmt.Errorf("项目必须暂停，且修复时 zrcin 必须已停止")
	}
	db, err := store.ReadSnapshot(filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		return err
	}
	ledger := map[string]model.Document{}
	for _, d := range db.Documents {
		switch d.Kind {
		case store.DocumentScope, store.DocumentConstraints:
			ledger[d.Kind] = d
		}
	}
	type field struct {
		Before, After string
		Revision      uint64 `json:",omitempty"`
		Changed       bool
	}
	plan := struct {
		Project     string   `json:"project"`
		Scope       *field   `json:"scope,omitempty"`
		Description *field   `json:"description,omitempty"`
		Unchanged   []string `json:"unchanged,omitempty"`
		Missing     []string `json:"ledger_missing,omitempty"`
	}{Project: manifest.ID}
	for _, c := range []struct {
		kind, name string
		current    string
		set        func(string)
	}{
		{store.DocumentScope, "scope", strings.TrimSpace(manifest.Scope), func(v string) { manifest.Scope = v }},
		{store.DocumentConstraints, "description", strings.TrimSpace(manifest.Description), func(v string) { manifest.Description = v }},
	} {
		doc, ok := ledger[c.kind]
		if !ok {
			// No ledger row: nothing audited to rebuild from; report it instead
			// of silently keeping or blanking the manifest text.
			plan.Missing = append(plan.Missing, c.name)
			continue
		}
		want := strings.TrimSpace(doc.Text)
		if want == c.current {
			plan.Unchanged = append(plan.Unchanged, c.name)
			continue
		}
		f := &field{Before: c.current, After: want, Revision: doc.Revision, Changed: true}
		if c.kind == store.DocumentScope {
			plan.Scope = f
		} else {
			plan.Description = f
		}
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		return err
	}
	if !commit {
		return nil
	}
	if plan.Scope == nil && plan.Description == nil {
		return fmt.Errorf("manifest 与账本一致，无需重建")
	}
	stamp := time.Now().Format("20060102-150405.000000000")
	backup := path + ".bak-" + stamp
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return err
	}
	if plan.Scope != nil {
		manifest.Scope = plan.Scope.After
	}
	if plan.Description != nil {
		manifest.Description = plan.Description.After
	}
	out, err := json.MarshalIndent(&manifest, "", "  ")
	if err != nil {
		return err
	}
	// Atomic replace, same shape the manager uses: a half-written project.json
	// would fail the next boot instead of finishing the repair.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "manifest 已按账本重建；修复前的文件备份在", backup)
	return nil
}
