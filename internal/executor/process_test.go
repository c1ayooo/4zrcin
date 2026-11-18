package executor

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// The stderr copier must never short-write or block, or the child stalls on a
// full pipe and Wait never returns. It also caps memory: a chatty process keeps
// its first 64 KiB, which is what explains a failure.
func TestCappedWriterNeverShortWritesAndCapsItsBody(t *testing.T) {
	var w cappedWriter
	chunk := []byte(strings.Repeat("x", 4096))
	for i := 0; i < 32; i++ {
		n, err := w.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("write %d: n=%d err=%v", i, n, err)
		}
	}
	if got := len(w.String()); got != cappedWriterLimit {
		t.Fatalf("kept %d bytes, want the %d-byte cap", got, cappedWriterLimit)
	}
}

// Regression: the stderr reader goroutine used to race Wait. When the child
// exited while that goroutine sat in Read, Wait closed the read end and the
// last line — the one that explains the failure — was lost.
func TestRunProcessKeepsStderrWrittenJustBeforeExit(t *testing.T) {
	bin, args := "sh", []string{"-c", "printf 'tail-line\\n' >&2; exit 3"}
	if runtime.GOOS == "windows" {
		bin, args = "cmd", []string{"/c", "echo tail-line 1>&2 & exit /b 3"}
	}
	res, err := runProcess(context.Background(), bin, args, t.TempDir(), nil, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || !strings.Contains(res.Output, "tail-line") {
		t.Fatalf("exit=%d output=%q, want exit 3 with the child's stderr", res.ExitCode, res.Output)
	}
}

// A cancellation closes our read end of the child's stdout. When the child
// happens to exit cleanly at that moment, Wait returns nil, and the old
// attribution called Error() on that nil waitErr — a panic that takes the whole
// control plane down. The stream was cut, so the run is a failure with a reason,
// never a success.
func TestTruncatedStreamIsNotSuccessAndDoesNotPanic(t *testing.T) {
	res := finishResult(Result{}, nil, "", true)
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1 for a truncated stream", res.ExitCode)
	}
	if !strings.Contains(res.Output, "读取 harness 事件流失败") {
		t.Fatalf("output does not explain the truncated stream: %q", res.Output)
	}
}

// The child's stderr explains why the stream died, but it must not erase the
// fact that the event stream itself was truncated.
func TestStderrExplainsATruncatedStream(t *testing.T) {
	res := finishResult(Result{}, nil, "harness: database is locked", true)
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Output, "读取 harness 事件流失败") ||
		!strings.Contains(res.Output, "database is locked") {
		t.Fatalf("output = %q, want the read failure and stderr", res.Output)
	}
}

// A clean exit keeps the child's stderr as the run's message (it is a warning,
// not a failure) and a failed exit with no stderr falls back to the exit status.
func TestFinishResultKeepsStderrAndExitStatus(t *testing.T) {
	clean := finishResult(Result{}, nil, "host warning: no working dir", false)
	if clean.ExitCode != 0 || clean.Output != "host warning: no working dir" {
		t.Fatalf("clean exit = %+v", clean)
	}

	failed := finishResult(Result{}, errors.New("exit status 3"), "", false)
	if failed.ExitCode != -1 || failed.Output != "exit status 3" {
		t.Fatalf("failed exit = %+v", failed)
	}
}
