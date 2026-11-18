// Command tw-metrics computes the seven P0-5 vertical metrics over one project
// directory and prints them as stable, sorted JSON or a human summary.
//
// It is a strictly read-only tool. The ledger is opened through
// store.ReadSnapshot (SQLite mode=ro, which cannot create or checkpoint files);
// if that fails because a live writer holds the file, the read is retried once
// and then falls back to a temporary copy of ledger.sqlite plus its -wal/-shm
// siblings. Nothing is ever written next to the project data, and the command
// fingerprints the files it read before and after so the output itself says
// whether the source bytes changed while it was reading.
//
// Usage:
//
//	tw-metrics --project <项目目录> [--json]
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kksty/tianwu/internal/metrics"
	"github.com/kksty/tianwu/internal/store"
)

// envelopeSchema is the stable top-level output contract of --json.
const envelopeSchema = "tw.metrics/v1"

// readSourceNames are the files the tool may read. They are also the set whose
// bytes are fingerprinted before/after the run.
var readSourceNames = []string{
	"ledger.sqlite",
	"ledger.sqlite-wal",
	"ledger.sqlite-shm",
	"decisions.jsonl",
	"operator-scope.jsonl",
}

// Counts records what the snapshot contained, so a zero metric can be read
// together with the amount of data behind it.
type Counts struct {
	Facts                  int  `json:"facts"`
	Intents                int  `json:"intents"`
	Runs                   int  `json:"runs"`
	Notebooks              int  `json:"notebooks"`
	Messages               int  `json:"messages"`
	Decisions              int  `json:"decisions"`
	DecisionsPresent       bool `json:"decisions_present"`
	DecisionsMalformed     int  `json:"decisions_malformed"`
	OperatorScope          int  `json:"operator_scope"`
	OperatorScopePresent   bool `json:"operator_scope_present"`
	OperatorScopeMalformed int  `json:"operator_scope_malformed"`
}

// Envelope is the --json document.
type Envelope struct {
	Schema          string           `json:"schema"`
	Tool            string           `json:"tool"`
	Project         string           `json:"project"`
	ReadMode        string           `json:"read_mode"`
	LedgerSchema    int              `json:"ledger_schema"`
	SourceUnchanged bool             `json:"source_unchanged"`
	ChangedFiles    []string         `json:"changed_files,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Counts          Counts           `json:"counts"`
	Metrics         []metrics.Metric `json:"metrics"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tw-metrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "项目目录（包含 ledger.sqlite）")
	asJSON := fs.Bool("json", false, "输出稳定排序 JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tw-metrics: 不接受位置参数：%s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(stderr, "tw-metrics: --project 必填")
		return 2
	}
	dir, err := filepath.Abs(*project)
	if err != nil {
		fmt.Fprintf(stderr, "tw-metrics: 解析项目路径失败：%v\n", err)
		return 1
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "tw-metrics: 项目目录不可读：%s\n", dir)
		return 1
	}

	// Fingerprint the read set before and after: unchanged bytes are the
	// read-only claim the tool can make about itself.
	before, err := fingerprintSources(dir)
	if err != nil {
		fmt.Fprintf(stderr, "tw-metrics: 读取源文件指纹失败：%v\n", err)
		return 1
	}

	db, readMode, warnings, err := loadLedger(dir)
	if err != nil {
		fmt.Fprintf(stderr, "tw-metrics: %v\n", err)
		return 1
	}
	decisions, decisionsPresent, decisionsMalformed, w := readDecisions(filepath.Join(dir, "decisions.jsonl"))
	warnings = append(warnings, w...)
	scope, scopePresent, scopeMalformed, w := readOperatorScope(filepath.Join(dir, "operator-scope.jsonl"))
	warnings = append(warnings, w...)

	snap := metrics.Snapshot{
		DB:                     db,
		Decisions:              decisions,
		DecisionsPresent:       decisionsPresent,
		DecisionsMalformed:     decisionsMalformed,
		OperatorScope:          scope,
		OperatorScopePresent:   scopePresent,
		OperatorScopeMalformed: scopeMalformed,
	}
	items := metrics.Compute(snap)

	after, err := fingerprintSources(dir)
	if err != nil {
		fmt.Fprintf(stderr, "tw-metrics: 读取源文件指纹失败：%v\n", err)
		return 1
	}
	changed := diffFingerprints(before, after)
	if len(changed) > 0 {
		warnings = append(warnings, "源文件在读取期间发生变化（可能有其他进程在写）；本输出只对应读取到的一致快照，未写回任何文件。")
	}

	env := Envelope{
		Schema:          envelopeSchema,
		Tool:            "tw-metrics",
		Project:         filepath.Base(dir),
		ReadMode:        readMode,
		LedgerSchema:    db.Version,
		SourceUnchanged: len(changed) == 0,
		ChangedFiles:    changed,
		Warnings:        warnings,
		Counts: Counts{
			Facts:                  len(db.Facts),
			Intents:                len(db.Intents),
			Runs:                   len(db.Runs),
			Notebooks:              len(db.Notebooks),
			Messages:               len(db.Messages),
			Decisions:              len(decisions),
			DecisionsPresent:       decisionsPresent,
			DecisionsMalformed:     decisionsMalformed,
			OperatorScope:          len(scope),
			OperatorScopePresent:   scopePresent,
			OperatorScopeMalformed: scopeMalformed,
		},
		Metrics: items,
	}
	if *asJSON {
		raw, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "tw-metrics: 编码 JSON 失败：%v\n", err)
			return 1
		}
		if _, err := fmt.Fprintf(stdout, "%s\n", raw); err != nil {
			return 1
		}
		return 0
	}
	writeHuman(stdout, env)
	return 0
}

// writeHuman prints the same numbers as the JSON in a stable one-metric-per-block
// layout; unavailable reasons are printed instead of a fake value.
func writeHuman(w io.Writer, env Envelope) {
	fmt.Fprintf(w, "tw-metrics schema=%s project=%s read_mode=%s ledger_schema=%d source_unchanged=%t\n",
		env.Schema, env.Project, env.ReadMode, env.LedgerSchema, env.SourceUnchanged)
	fmt.Fprintf(w, "counts: facts=%d intents=%d runs=%d decisions=%d(present=%t) operator_scope=%d(present=%t)\n",
		env.Counts.Facts, env.Counts.Intents, env.Counts.Runs,
		env.Counts.Decisions, env.Counts.DecisionsPresent,
		env.Counts.OperatorScope, env.Counts.OperatorScopePresent)
	for _, m := range env.Metrics {
		fmt.Fprintf(w, "\n[%d] %s\n", m.Order, m.Key)
		if m.Value == nil {
			fmt.Fprintf(w, "  value: unavailable(%s) numerator=%d denominator=%d unit=%s\n", m.Unavailable, m.Numerator, m.Denominator, m.Unit)
		} else {
			fmt.Fprintf(w, "  value: %v %s (numerator=%d denominator=%d)\n", *m.Value, m.Unit, m.Numerator, m.Denominator)
		}
		if m.Explanation != "" {
			fmt.Fprintf(w, "  explanation: %s\n", m.Explanation)
		}
		fmt.Fprintf(w, "  definition: %s\n", m.Definition)
		if len(m.Breakdown) > 0 {
			keys := make([]string, 0, len(m.Breakdown))
			for k := range m.Breakdown {
				keys = append(keys, k)
			}
			sortStrings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%d", k, m.Breakdown[k]))
			}
			fmt.Fprintf(w, "  breakdown: %s\n", strings.Join(parts, " "))
		}
		if len(m.Details) > 0 {
			parts := make([]string, 0, len(m.Details))
			for _, d := range m.Details {
				parts = append(parts, fmt.Sprintf("%s=%v%s", d.Name, d.Value, detailUnitSuffix(d.Unit)))
			}
			fmt.Fprintf(w, "  details: %s\n", strings.Join(parts, " "))
		}
	}
	if len(env.Warnings) > 0 {
		fmt.Fprintf(w, "\nwarnings:\n")
		for _, x := range env.Warnings {
			fmt.Fprintf(w, "- %s\n", x)
		}
	}
}

func detailUnitSuffix(unit string) string {
	if unit == "" || unit == "count" {
		return ""
	}
	return " " + unit
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// loadLedger opens the committed ledger without letting the reader write to it.
// A non-empty WAL means the newest committed rows still live beside the main
// file, so the tool reads a temporary copy (which includes the WAL/SHM rows) and
// never opens the source writable. Otherwise it reads the source through
// SQLite's immutable URI mode, which creates no journal/lock files at all. A
// failed direct read is retried once (the file may be momentarily inaccessible)
// and then falls back to the same temporary copy.
func loadLedger(projectDir string) (store.DB, string, []string, error) {
	ledgerPath := filepath.Join(projectDir, "ledger.sqlite")
	info, err := os.Stat(ledgerPath)
	if err != nil {
		return store.DB{}, "", nil, fmt.Errorf("账本不可读：%s：%w", ledgerPath, err)
	}
	if info.IsDir() || info.Size() == 0 {
		return store.DB{}, "", nil, fmt.Errorf("账本为空或不是文件：%s", ledgerPath)
	}
	var warnings []string
	if ledgerNeedsCopy(ledgerPath) {
		db, err := readLedgerCopy(ledgerPath)
		if err != nil {
			return store.DB{}, "", nil, fmt.Errorf("账本存在非空 WAL，复制临时副本后读取失败：%w", err)
		}
		warnings = append(warnings, "账本存在非空 -wal（可能被活跃写者持有）：已复制临时目录（含 -wal/-shm）后读取，未改动源文件。")
		return db, "temp_copy", warnings, nil
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		db, err := readLedgerImmutable(ledgerPath)
		if err == nil {
			if attempt > 0 {
				warnings = append(warnings, "账本第一次只读打开失败（可能短暂被占用），重试一次后成功。")
			}
			return db, "direct_immutable", warnings, nil
		}
		lastErr = err
	}
	db, err := readLedgerCopy(ledgerPath)
	if err != nil {
		return store.DB{}, "", nil, fmt.Errorf("以 immutable 只读模式打开账本失败：%v；复制临时副本后读取也失败：%w", lastErr, err)
	}
	warnings = append(warnings, "账本 immutable 直读两次均失败，已复制临时目录（含 -wal/-shm）后读取；未改动源文件。")
	return db, "temp_copy", warnings, nil
}

// readLedgerCopy copies ledger.sqlite plus any WAL/SHM siblings to a fresh temp
// dir and reads that copy read-only. The temp dir is removed before returning.
func readLedgerCopy(ledgerPath string) (store.DB, error) {
	tmp, err := os.MkdirTemp("", "tw-metrics-ledger-*")
	if err != nil {
		return store.DB{}, err
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "ledger.sqlite")
	if err := copyFile(ledgerPath, dst); err != nil {
		return store.DB{}, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		src := ledgerPath + suffix
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, dst+suffix); err != nil {
			return store.DB{}, err
		}
	}
	return store.ReadSnapshot(dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// readDecisions parses decisions.jsonl tolerantly: a missing file is not an
// error (older or non-commander projects have none), invalid lines are counted
// and skipped. A trailing partial line therefore degrades to one malformed count
// instead of losing the whole measure.
func readDecisions(path string) ([]metrics.Decision, bool, int, []string) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, 0, nil
		}
		return nil, false, 0, []string{"读取 decisions.jsonl 失败，按缺失处理：" + err.Error()}
	}
	defer f.Close()
	var out []metrics.Decision
	malformed := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var d metrics.Decision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			malformed++
			continue
		}
		out = append(out, d)
	}
	warnings := []string(nil)
	if err := sc.Err(); err != nil {
		warnings = append(warnings, "读取 decisions.jsonl 中断，已保留已解析行："+err.Error())
	}
	return out, true, malformed, warnings
}

// readOperatorScope parses the P0-4 append-only operator-scope.jsonl by its
// published schema. A missing file is a normal historical state and is reported
// as absent; malformed lines are counted and skipped (the file is append-only,
// so a crash can leave a partial tail line).
func readOperatorScope(path string) ([]metrics.OperatorScopeEntry, bool, int, []string) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, 0, nil
		}
		return nil, false, 0, []string{"读取 operator-scope.jsonl 失败，按缺失处理：" + err.Error()}
	}
	defer f.Close()
	var out []metrics.OperatorScopeEntry
	malformed := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e metrics.OperatorScopeEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			malformed++
			continue
		}
		out = append(out, e)
	}
	warnings := []string(nil)
	if err := sc.Err(); err != nil {
		warnings = append(warnings, "读取 operator-scope.jsonl 中断，已保留已解析行："+err.Error())
	}
	return out, true, malformed, warnings
}

// fingerprintSources hashes the read set (missing files are recorded as "-") so
// the command can state whether any source byte changed while it was reading.
func fingerprintSources(projectDir string) (map[string]string, error) {
	out := make(map[string]string, len(readSourceNames))
	for _, name := range readSourceNames {
		path := filepath.Join(projectDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				out[name] = "-"
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("源文件是目录：%s", path)
		}
		sum, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		out[name] = sum
	}
	return out, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func diffFingerprints(before, after map[string]string) []string {
	var changed []string
	for _, name := range readSourceNames {
		if before[name] != after[name] {
			changed = append(changed, name)
		}
	}
	return changed
}
