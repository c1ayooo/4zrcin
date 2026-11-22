package bench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The four answers the platform is allowed to give, and what each one must mean
// to the loop. Getting this table wrong is how an eight-hour run turns into
// thirty thousand retries of a request the platform already rejected.
func TestClassifyPlatformAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   Kind
		fails  bool
	}{
		{name: "success", status: http.StatusOK, want: KindTerminal, fails: false},
		{name: "resource_unavailable is retryable", status: http.StatusServiceUnavailable, code: CodeResourceUnavailable, want: KindRetryable, fails: true},
		{name: "503 without a code is retryable", status: http.StatusServiceUnavailable, want: KindRetryable, fails: true},
		{name: "duplicate is success", status: http.StatusConflict, code: CodeDuplicate, want: KindDuplicate, fails: true},
		{name: "invalid_state stops the run", status: http.StatusConflict, code: CodeInvalidState, want: KindTerminal, fails: true},
		{name: "an unclassified 500 stops the run", status: http.StatusInternalServerError, want: KindTerminal, fails: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Classify(tc.status, tc.code, "fixture")
			if tc.fails != (err != nil) {
				t.Fatalf("Classify(%d,%q) = %v, want error=%v", tc.status, tc.code, err, tc.fails)
			}
			if got := KindOf(err); got != tc.want {
				t.Fatalf("KindOf = %v, want %v", got, tc.want)
			}
		})
	}
	// A dead connection or a decoding bug is not a platform answer: it must stop
	// the run rather than be retried as if the platform had asked for patience.
	if got := KindOf(errors.New("connection reset")); got != KindTerminal {
		t.Fatalf("KindOf(transport error) = %v, want terminal", got)
	}
	if got := KindOf(nil); got != KindTerminal {
		t.Fatalf("KindOf(nil) = %v, want terminal", got)
	}
}

// A fourth container must wait for a release instead of being started and
// refused. The platform's 409 is the backstop; this gate is the plan.
func TestGateQueuesInsteadOfOvertaking(t *testing.T) {
	ctx := context.Background()
	g := NewGate(SlotLimit)
	for i := 0; i < SlotLimit; i++ {
		if err := g.Acquire(ctx); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if g.InUse() != SlotLimit || g.Limit() != SlotLimit {
		t.Fatalf("gate = %d/%d, want %d/%d", g.InUse(), g.Limit(), SlotLimit, SlotLimit)
	}

	first := make(chan string, 1)
	second := make(chan string, 1)
	go func() {
		if err := g.Acquire(ctx); err != nil {
			first <- "error: " + err.Error()
			return
		}
		first <- "admitted"
	}()
	go func() {
		time.Sleep(10 * time.Millisecond) // let the first waiter queue up ahead
		if err := g.Acquire(ctx); err != nil {
			second <- "error: " + err.Error()
			return
		}
		second <- "admitted"
	}()

	// Absence assertion: with every slot held, neither waiter may slip in.
	select {
	case got := <-first:
		t.Fatalf("a waiter entered while the gate was full: %s", got)
	case <-time.After(60 * time.Millisecond):
	}
	if g.InUse() != SlotLimit {
		t.Fatalf("gate = %d while full, want %d", g.InUse(), SlotLimit)
	}

	g.Release()
	select {
	case got := <-first:
		if got != "admitted" {
			t.Fatalf("first waiter: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first waiter was not admitted after a release")
	}
	// The one release went to the first waiter, not to the second.
	select {
	case got := <-second:
		t.Fatalf("second waiter overtook the first: %s", got)
	case <-time.After(60 * time.Millisecond):
	}
	g.Release()
	if got := <-second; got != "admitted" {
		t.Fatalf("second waiter: %s", got)
	}
}

// Every attempt leaves exactly one line, and the raw flag never does: the log is
// the artifact that leaves the machine.
func TestSubmitLogWritesOneLinePerAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submissions.jsonl")
	log, err := OpenSubmitLog(path)
	if err != nil {
		t.Fatal(err)
	}
	flag := "flag{fixture-A}"
	accepted := Submission{
		UniqueCode: "ch_fixture_a",
		FlagSHA256: DigestFlag(flag),
		FlagLength: len(flag),
		Correct:    true,
		Awarded:    100,
		Cumulative: 100,
		At:         time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC),
	}
	rejected := Submission{
		UniqueCode: "ch_fixture_b",
		FlagSHA256: DigestFlag("flag{fixture-B}"),
		FlagLength: len("flag{fixture-B}"),
		Correct:    false,
		Cumulative: 100,
		At:         accepted.At.Add(time.Minute),
	}
	for _, s := range []Submission{accepted, rejected} {
		if err := log.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if log.Path() != path {
		t.Fatalf("Path() = %q, want %q", log.Path(), path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want one per attempt (2)", len(lines))
	}
	var got Submission
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	if got.UniqueCode != accepted.UniqueCode || !got.Correct || got.Awarded != 100 ||
		got.Cumulative != 100 || got.FlagSHA256 != DigestFlag(flag) || got.FlagLength != len(flag) {
		t.Fatalf("first line = %+v, want the accepted attempt", got)
	}
	if strings.Contains(string(raw), flag) {
		t.Fatalf("the log leaked a raw flag: %s", raw)
	}
	// A wrong submission is recorded too: the score is only explainable if the
	// misses are in the same file as the hits.
	var miss Submission
	if err := json.Unmarshal([]byte(lines[1]), &miss); err != nil {
		t.Fatal(err)
	}
	if miss.Correct || miss.Awarded != 0 {
		t.Fatalf("second line = %+v, want a recorded miss", miss)
	}
}
