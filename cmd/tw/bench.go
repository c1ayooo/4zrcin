package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kksty/tianwu/internal/bench"
	"github.com/kksty/tianwu/internal/connectretry"
)

// benchRunner drives one measured run from outside the control plane: it polls
// the platform, turns every open challenge into a goal, starts containers, and
// keeps the project's scope document equal to the platform's current address set.
//
// It talks to a running `tw` over HTTP with the console token. It never touches
// the engine directly, so a normal project's pause semantics cannot be changed by
// a benchmark run — the only lever is the scope document, written with its own
// author name.
type benchRunner struct {
	console  string
	token    string
	project  string
	interval time.Duration
	once     bool
	flagRe   *regexp.Regexp

	client  *bench.Client
	http    *http.Client
	attempt *bench.AttemptLog
	submit  *bench.SubmitLog
	seen    map[string]bool // submitted (correct or not) per challenge+digest

	state bench.State
}

func cliBench(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: tw bench [flags]")
		fmt.Fprintln(stdout, "  把 TSecBench 平台上的题目变成天吴的目标，并把可用地址写进项目起点。")
		fmt.Fprintln(stdout, "  只做四步：轮询挑战 → 对账 → 建目标/start/写起点 → 等下一轮；退出时按最后一份快照重写起点。")
		fmt.Fprintln(stdout, "  需要 BENCHMARK_BASE_URL 与 BENCHMARK_TOKEN。")
		fmt.Fprintln(stdout, "flags:")
		fs.PrintDefaults()
	}
	var (
		console    = fs.String("console", "http://127.0.0.1:8080", "运行中的天吴控制面地址")
		token      = fs.String("token", "", "控制台登录令牌（默认读 TW_CONSOLE_TOKEN，或 -data 下的 console.token）")
		tokenFile  = fs.String("token-file", "", "从文件读控制台登录令牌")
		dataDir    = fs.String("data", "", "实例数据目录：用于找 console.token，并作为日志默认位置")
		project    = fs.String("project", "default", "写入哪个项目的目标与起点")
		dumpDir    = fs.String("dump-dir", "", "challenges 原始响应落盘目录（默认 <data>/bench/challenges）")
		submitLogP = fs.String("submit-log", "", "提交器日志路径（默认 <data>/bench/submissions.jsonl）")
		attemptLog = fs.String("attempt-log", "", "逐次尝试日志路径（默认 <data>/bench/attempts.jsonl）")
		interval   = fs.Duration("interval", 30*time.Second, "对账周期")
		once       = fs.Bool("once", false, "只跑一轮（调试用）")
		flagRegex  = fs.String("flag-regex", bench.DefaultFlagPattern, "从账本事实里提取 flag 的正则；平台格式不同就换这里")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error=invalid_args 无法识别的参数：%v\n", fs.Args())
		return 2
	}
	re, err := regexp.Compile(*flagRegex)
	if err != nil {
		fmt.Fprintf(stderr, "error=bad_flag_regex %v\n", err)
		return 2
	}
	base, benchToken := getenv("BENCHMARK_BASE_URL"), getenv("BENCHMARK_TOKEN")
	if base == "" || benchToken == "" {
		fmt.Fprintln(stderr, "error=bench_not_configured 需要 BENCHMARK_BASE_URL 与 BENCHMARK_TOKEN 两个环境变量；不配就不启用测评接入")
		return 2
	}

	// Resolve the console token: flag, file, env, then the instance's own file.
	tok := *token
	if tok == "" && *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintf(stderr, "error=token_file %v\n", err)
			return 2
		}
		tok = strings.TrimSpace(string(b))
	}
	if tok == "" {
		tok = strings.TrimSpace(getenv("TW_CONSOLE_TOKEN"))
	}
	if tok == "" && *dataDir != "" {
		if b, err := os.ReadFile(filepath.Join(*dataDir, "console.token")); err == nil {
			tok = strings.TrimSpace(string(b))
		}
	}
	if tok == "" {
		fmt.Fprintln(stderr, "error=no_console_token 需要 -token/-token-file/TW_CONSOLE_TOKEN，或 -data 指向一个有 console.token 的实例")
		return 2
	}

	logDir := *dataDir
	if *dumpDir == "" && logDir != "" {
		*dumpDir = filepath.Join(logDir, "bench", "challenges")
	}
	if *submitLogP == "" && logDir != "" {
		*submitLogP = filepath.Join(logDir, "bench", "submissions.jsonl")
	}
	if *attemptLog == "" && logDir != "" {
		*attemptLog = filepath.Join(logDir, "bench", "attempts.jsonl")
	}
	for _, p := range []*string{submitLogP, attemptLog} {
		if *p != "" {
			if err := os.MkdirAll(filepath.Dir(*p), 0o755); err != nil {
				fmt.Fprintf(stderr, "error=log_dir %v\n", err)
				return 2
			}
		}
	}

	client, err := bench.NewClient(bench.Config{BaseURL: base, Token: benchToken, DumpDir: *dumpDir})
	if err != nil {
		fmt.Fprintf(stderr, "error=platform_client %v\n", err)
		return 2
	}
	r := &benchRunner{
		console: *console, token: tok, project: *project, interval: *interval, once: *once,
		flagRe: re, client: client, http: &http.Client{Timeout: 60 * time.Second}, seen: map[string]bool{},
	}
	if *attemptLog != "" {
		if r.attempt, err = bench.OpenAttemptLog(*attemptLog); err != nil {
			fmt.Fprintf(stderr, "error=attempt_log %v\n", err)
			return 2
		}
		defer r.attempt.Close()
	}
	if *submitLogP != "" {
		if r.submit, err = bench.OpenSubmitLog(*submitLogP); err != nil {
			fmt.Fprintf(stderr, "error=submit_log %v\n", err)
			return 2
		}
		defer r.submit.Close()
	}
	// Anything already in the submit log is never sent again — correct or not.
	// Re-submitting a wrong flag burns an attempt per poll for no information.
	if *submitLogP != "" {
		r.loadSubmitted(*submitLogP)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := r.run(ctx, stderr)
	r.finish(ctx, stderr)
	return code
}

// initStarted lazily creates the started set: State is a cache of what was
// already asked, and Plan reads it to tell ""waiting for a start"" from ""the
// start did not take"".
func (r *benchRunner) initStarted() {
	if r.state.Started == nil {
		r.state.Started = map[string]bool{}
	}
	if r.state.GoalsEnsured == nil {
		r.state.GoalsEnsured = map[string]bool{}
	}
}

func (r *benchRunner) loadSubmitted(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s bench.Submission
		if json.Unmarshal([]byte(line), &s) == nil && s.FlagSHA256 != "" {
			r.seen[s.UniqueCode+"|"+s.FlagSHA256] = true
		}
	}
}

// run is the four-step loop. It returns the process exit code; finish() runs
// afterwards in every case, including failures.
func (r *benchRunner) run(ctx context.Context, stderr io.Writer) int {
	for {
		snap, err := r.poll(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error=poll %v\n", err)
			return 1
		}
		r.initStarted()
		steps := bench.Plan(r.state, snap, r.client.LastDumpFile())
		started := false
		for _, step := range steps {
			switch step.Kind {
			case bench.StepEnsureGoal:
				if err := r.ensureGoal(ctx, step.Code, step.Text); err != nil {
					fmt.Fprintf(stderr, "error=goal %s: %v\n", step.Code, err)
					return 1
				}
				r.state.GoalsEnsured[step.Code] = true
			case bench.StepStart:
				if err := r.startChallenge(ctx, step.Code); err != nil {
					var f *bench.Failure
					if errors.As(err, &f) && bench.IsSlotFull(err) {
						// Three containers are already live. Wait for the next poll
						// instead of fighting for a slot.
						r.logAttempt("start:"+step.Code, 1, err, 0)
						continue
					}
					fmt.Fprintf(stderr, "error=start %s: %v\n", step.Code, err)
					return 1
				}
				r.state.Started[step.Code] = true
				started = true
			case bench.StepWriteScope:
				if err := r.writeScope(ctx, step.Text, len(step.Addrs)); err != nil {
					// A scope that could not be written must stop the run: the
					// commander would otherwise keep reading the previous address
					// list, which may already belong to other challenges.
					fmt.Fprintf(stderr, "error=scope %v\n", err)
					return 1
				}
				r.state.ScopeAddrs = step.Addrs
			case bench.StepStop:
				fmt.Fprintf(stderr, "info=stop reason=%s %s\n", step.Code, step.Reason)
				if step.Code == "all_completed" {
					return 0
				}
				return 1
			}
		}
		if err := r.submitFacts(ctx, snap); err != nil {
			fmt.Fprintf(stderr, "error=submit %v\n", err)
		}
		if started {
			// The new container's address is not in any snapshot yet; poll again
			// before the next decision so the scope is written from real bytes.
			continue
		}
		if r.once {
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(r.interval):
		}
	}
}

// finish rewrites the scope from the last snapshot the run actually saw, and
// releases the containers it still holds. Both are best effort, but a scope that
// cannot be rewritten is reported loudly: leaving the previous address list is
// the most dangerous state this run can end in.
func (r *benchRunner) finish(ctx context.Context, stderr io.Writer) {
	snap, err := r.poll(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error=finish_poll %v（起点保持上一次写入的内容，需人工确认）\n", err)
		return
	}
	for _, ch := range snap.Active() {
		if _, err := r.client.CloseChallenge(ctx, ch.UniqueCode); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "warn=finish_close %s: %v\n", ch.UniqueCode, err)
		}
	}
	// Close then re-read: closing is what frees the addresses, so the final text
	// must come from a snapshot taken after it.
	snap, err = r.poll(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error=finish_poll_after_close %v\n", err)
		return
	}
	text, err := bench.FinalScope(snap, r.client.LastDumpFile())
	if err != nil {
		fmt.Fprintf(stderr, "error=finish_scope %v\n", err)
		return
	}
	if err := r.writeScope(ctx, text, len(snap.AllowedAddrs())); err != nil {
		fmt.Fprintf(stderr, "error=finish_scope_write %v（起点仍是上一次写入的内容，需人工确认）\n", err)
	}
}

// poll reads the challenge list with the connect-class retry budget.
func (r *benchRunner) poll(ctx context.Context) (bench.Snapshot, error) {
	var snap bench.Snapshot
	err := r.withRetry("challenges", func() error {
		list, err := r.client.ListChallenges(ctx)
		var de *bench.DumpError
		if errors.As(err, &de) {
			if r.attempt != nil {
				_ = r.attempt.Append(bench.Attempt{Op: "dump_challenges", Attempt: 1, OK: false,
					Kind: "dump_failed", Error: de.Error()})
			}
			err = nil // the poll itself worked; the missing file is recorded above
		}
		if err != nil {
			return err
		}
		snap = bench.NewSnapshot(time.Now(), list)
		return nil
	})
	return snap, err
}

func (r *benchRunner) startChallenge(ctx context.Context, code string) error {
	return r.withRetry("start:"+code, func() error {
		_, err := r.client.StartChallenge(ctx, code)
		return err
	})
}

// withRetry repeats only what a repeat can fix (connect class / 5xx), within the
// shared budget, and records every attempt. Business refusals are returned at
// once: the platform understood the request and said no.
func (r *benchRunner) withRetry(op string, fn func() error) error {
	deadline := time.Now().Add(connectretry.Window)
	for attempt := 1; ; attempt++ {
		start := time.Now()
		err := fn()
		r.logAttempt(op, attempt, err, time.Since(start))
		if err == nil || !bench.Retryable(err) || time.Now().After(deadline) {
			return err
		}
		if w := connectretry.WaitForNext(attempt, deadline); w > 0 {
			time.Sleep(w)
		}
	}
}

func (r *benchRunner) logAttempt(op string, attempt int, err error, d time.Duration) {
	if r.attempt == nil {
		return
	}
	a := bench.Attempt{Op: op, Attempt: attempt, OK: err == nil, Milli: d.Milliseconds()}
	if err != nil {
		a.Kind = bench.KindOf(err).String()
		a.Error = err.Error()
	}
	_ = r.attempt.Append(a)
}

// Console calls. Every write carries the bench signature so the ledger and the
// audit can tell a machine's narrowing of the target set from the operator's own
// edit; the HTTP route still admits only the operator's credential.

func (r *benchRunner) projectURL(path string) string {
	return strings.TrimRight(r.console, "/") + "/p/" + r.project + path
}

func (r *benchRunner) call(ctx context.Context, op, method, url string, body any, headers map[string]string) ([]byte, error) {
	var raw []byte
	err := r.withRetry(op, func() error {
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+r.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := r.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var env struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			_ = json.Unmarshal(out, &env)
			if env.Error == "" {
				env.Error = strings.TrimSpace(string(out))
			}
			return fmt.Errorf("console %d %s: %s", resp.StatusCode, env.Code, env.Error)
		}
		raw = out
		return nil
	})
	return raw, err
}

func (r *benchRunner) ensureGoal(ctx context.Context, code, statement string) error {
	_, err := r.call(ctx, "append_goal:"+code, http.MethodPost, r.projectURL("/api/project/append-goal"),
		map[string]any{
			"statement": statement,
			// Addresses live in the scope document; the goal stays stable so
			// replacing a container never rewrites a completion receipt.
			"start":           "本题容器地址见项目起点（由平台快照刷新）",
			"idempotency_key": "bench-goal:" + code,
		},
		map[string]string{bench.ActorHeader: bench.ActorBench})
	return err
}

func (r *benchRunner) writeScope(ctx context.Context, text string, addrs int) error {
	raw, err := r.call(ctx, "documents", http.MethodGet, r.projectURL("/api/documents"), nil, nil)
	if err != nil {
		return err
	}
	var view struct {
		Documents map[string]struct {
			Revision uint64 `json:"revision"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		return err
	}
	rev := view.Documents["scope"].Revision
	_, err = r.call(ctx, "scope_write", http.MethodPut, r.projectURL("/api/documents/scope"), map[string]any{
		"text":        text,
		"revision":    rev,
		"evidence_id": filepath.Base(r.client.LastDumpFile()),
		"note":        fmt.Sprintf("地址 %d 个", addrs),
	}, map[string]string{bench.ActorHeader: bench.ActorBench})
	return err
}

// submitFacts submits the flags executors declared, one platform call per flag.
// The fact's entity is what binds a flag to a challenge; when it does not match a
// currently available container the flag is skipped and logged, because a guess
// would submit against the wrong challenge.
func (r *benchRunner) submitFacts(ctx context.Context, snap bench.Snapshot) error {
	raw, err := r.call(ctx, "facts", http.MethodGet, r.projectURL("/api/facts?limit=500"), nil, nil)
	if err != nil {
		return err
	}
	var page struct {
		Facts []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Summary   string `json:"summary"`
			EntityRef string `json:"entity_ref"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return err
	}
	active := snap.Active()
	snapAddr := snap.AllowedAddrs()
	for _, f := range page.Facts {
		flags := bench.ExtractFlags(f.Title+"\n"+f.Summary, r.flagRe)
		if len(flags) == 0 {
			continue
		}
		code, ok := bench.ResolveChallenge(f.EntityRef, active)
		if !ok {
			r.logAttempt("submit_skip:"+f.ID, 1, fmt.Errorf("entity %q 不对应任何 available 容器（可用：%v）", f.EntityRef, snapAddr), 0)
			continue
		}
		for _, flag := range flags {
			digest := bench.DigestFlag(flag)
			key := code + "|" + digest
			if r.seen[key] {
				continue
			}
			res, err := r.client.SubmitFlag(ctx, code, flag)
			sub := bench.Submission{UniqueCode: code, FlagSHA256: digest, FlagLength: len(flag)}
			switch {
			case err == nil && res != nil:
				sub.Correct, sub.Awarded, sub.Cumulative = res.Correct, res.Awarded, res.CumulativeScore
				if res.MatchedFlagIndex != nil {
					sub.Note = fmt.Sprintf("matched_flag_index=%d fact=%s", *res.MatchedFlagIndex, f.ID)
				} else {
					sub.Note = "fact=" + f.ID
				}
			case err != nil && bench.KindOf(err) == bench.KindDuplicate:
				sub.Correct, sub.Note = true, "duplicate（平台已计）fact="+f.ID
			default:
				sub.Note = "error=" + err.Error() + " fact=" + f.ID
				r.logAttempt("submit:"+code, 1, err, 0)
			}
			if r.submit != nil {
				if aerr := r.submit.Append(sub); aerr != nil {
					return aerr
				}
			}
			// Wrong flags are remembered too: retrying the same wrong value every
			// poll would spend the whole run on one mistake.
			r.seen[key] = true
		}
	}
	return nil
}
