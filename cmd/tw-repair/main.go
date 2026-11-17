// tw-repair is an explicit offline cursor repair. It never starts a model or
// changes ledger facts. Run with the control plane stopped; dry-run is default.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/kksty/tianwu/internal/ledger"
	"github.com/kksty/tianwu/internal/project"
	"github.com/kksty/tianwu/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	fs := flag.NewFlagSet("tw-repair", flag.ContinueOnError)
	dir := fs.String("project", "", "暂停项目的绝对目录（先停止 tw）")
	intent := fs.String("intent", "", "精确的方向 ID")
	commit := fs.Bool("commit", false, "核对全部事实后保存游标；默认只预览")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The directory must be absolute because the fix edits it while tw is
	// stopped; the intent must be one path segment because it is also a folder.
	if !filepath.IsAbs(*dir) || *intent == "" || *intent == "." || *intent == ".." || filepath.Base(*intent) != *intent || strings.ContainsAny(*intent, "/\\") {
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
		return fmt.Errorf("项目必须暂停，且修复时 tw 必须已停止")
	}
	db, err := store.ReadSnapshot(filepath.Join(*dir, "ledger.sqlite"))
	if err != nil {
		return err
	}
	known := false
	for _, in := range db.Intents {
		if in.ID == *intent {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("项目中没有方向 %s", *intent)
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
