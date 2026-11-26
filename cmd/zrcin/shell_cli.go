package main

// zrcin shell run --task <file>: the node-side executor of the webshell
// operation path (docs/18). It is driven from the local host's direct command
// channel — the console sends "!<zrcin> shell run --task …", the same way it
// drives browser control. The task file is a one-time ticket: it is deleted
// before anything runs, so a retry cannot replay an operation that may have
// executed; the credential never reaches argv, a log line or the receipt, and
// the receipt is one JSON object on stdout.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// shellCLITimeout bounds one operation. It is longer than the console's
// ShellWait on purpose: the console reports a timeout while a slow removal may
// still be finishing on the target, and the receipt is simply dropped.
const shellCLITimeout = 2 * time.Minute

// maybeShellCLI takes over when the first args name the shell verb. It runs and
// exits: unlike the read verbs it performs operations, so it never falls through
// to server flag parsing.
func maybeShellCLI(args []string) bool {
	if len(args) < 2 || args[0] != "shell" {
		return false
	}
	if len(args) < 2 || args[1] != "run" {
		fmt.Fprintf(os.Stderr, "error=invalid_args zrcin shell 的子命令只有 run\n")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("shell run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	task := fs.String("task", "", "任务文件路径（0600，一次性）")
	if err := fs.Parse(args[2:]); err != nil {
		os.Exit(2)
	}
	if code := runShellCLI(*task, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
	return true
}

// runShellCLI is the testable entry point: same behaviour, caller-owned streams.
// A shell-level failure is still exit 0 — the receipt carries it; only a local
// failure (no task, undecodable task) exits non-zero.
func runShellCLI(taskPath string, stdout, stderr io.Writer) int {
	// Local failures go to stdout as well: the command channel that drives this
	// executor may only capture stdout, and the caller needs the reason.
	fail := func(code int, format string, args ...any) int {
		fmt.Fprintf(stdout, "error="+format+"\n", args...)
		fmt.Fprintf(stderr, "error="+format+"\n", args...)
		return code
	}
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		return fail(1, "task_read %v", err)
	}
	// Gone before anything runs.
	_ = os.Remove(taskPath)
	var task enroll.ShellTask
	if err := json.Unmarshal(raw, &task); err != nil {
		return fail(1, "task_decode %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellCLITimeout)
	defer cancel()
	res, runErr := webshell.Run(ctx, webshell.NewDeepClient(shellCLIRequestTimeout), task.URL, task.Credential, task.Op)
	receipt := enroll.ShellResult{RequestID: task.RequestID, Result: &res}
	if runErr != nil {
		receipt.Error = runErr.Error()
	}
	if err := json.NewEncoder(stdout).Encode(receipt); err != nil {
		return fail(1, "receipt_encode %v", err)
	}
	return 0
}

const shellCLIRequestTimeout = 20 * time.Second
