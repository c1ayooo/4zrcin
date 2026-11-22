package testutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	Install()
	os.Exit(m.Run())
}

// A registered host is answered in-process: no connection is opened, so the
// assertion cannot be affected by the host's loopback stack.
func TestRegisteredHostIsServedInProcess(t *testing.T) {
	srv := NewServerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("X-Test", "1")
		_, _ = io.WriteString(w, "ok")
	})
	defer srv.Close()
	res, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatalf("in-process request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "ok" || res.Header.Get("X-Test") != "1" {
		t.Fatalf("got %d %q %q", res.StatusCode, body, res.Header.Get("X-Test"))
	}
}

// An unregistered host keeps the real transport: the in-process layer must not
// swallow unrelated traffic.
func TestUnregisteredHostStillUsesTheRealTransport(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener available: %v", err)
	}
	addr := l.Addr().String()
	l.Close() // closed port: the dial must be attempted and refused
	if _, err = http.Get("http://" + addr + "/"); err == nil {
		t.Fatal("request to a closed port succeeded")
	}
}

// A refused dial is final: waiting cannot make a listener appear, so a test
// that expects the refusal must not spend the retry budget on it.
func TestDialRetryDoesNotRetryARefusal(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener available: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	before := len(retrySnapshot())
	ReportDialRetries(t)

	client := &http.Client{Transport: http.DefaultTransport.(inProcessTransport).base}
	if _, err = client.Get("http://" + addr + "/"); err == nil {
		t.Fatal("request to a closed port succeeded")
	}
	if events := retrySnapshot()[before:]; len(events) != 0 {
		t.Fatalf("a refused dial was retried %d times: %v", len(events), events)
	}
}

// A connect that times out is the failure the host causes, so it is waited out
// on the given schedule and every retry is recorded.
func TestDialRetryWaitsOutATimeout(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	var attempts int
	dial := retryDialContext(func(context.Context, string, string) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: dialTimeoutErr{}}
		}
		return left, nil
	}, []time.Duration{time.Millisecond, time.Millisecond}, time.Second)

	before := len(retrySnapshot())
	conn, err := dial(context.Background(), "tcp", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("two connect timeouts were not recovered: %v", err)
	}
	defer conn.Close()
	if attempts != 3 {
		t.Fatalf("dial attempts = %d, want 3 (one first connect + two retries)", attempts)
	}
	if events := retrySnapshot()[before:]; len(events) != 2 {
		t.Fatalf("recorded %d retries, want 2", len(events))
	}
}

// The window is a ceiling on the whole phase: a schedule longer than it is
// clipped, so a dead port cannot drag a test past the budget.
func TestDialRetryStopsAtTheWindow(t *testing.T) {
	var attempts int
	dial := retryDialContext(func(context.Context, string, string) (net.Conn, error) {
		attempts++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: dialTimeoutErr{}}
	}, []time.Duration{time.Second}, 20*time.Millisecond)

	start := time.Now()
	_, err := dial(context.Background(), "tcp", "127.0.0.1:1")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a port that never answered was reported as connected")
	}
	if attempts != 2 {
		t.Fatalf("dial attempts = %d, want 2 (the 1s wait was clipped to the 20ms window)", attempts)
	}
	if elapsed > time.Second {
		t.Fatalf("dial spent %s, want the window as the ceiling", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out (budget") {
		t.Fatalf("error does not state the budget: %v", err)
	}
}

// dialTimeoutErr is the shape a dropped SYN produces: a net.Error that says
// "timeout", which is the only dial failure this helper retries.
type dialTimeoutErr struct{}

func (dialTimeoutErr) Error() string   { return "i/o timeout" }
func (dialTimeoutErr) Timeout() bool   { return true }
func (dialTimeoutErr) Temporary() bool { return true }

// The package summary is the only place a passing run ever shows a retry: it
// must stay silent when nothing was retried and print one count line otherwise.
func TestSummaryReportsOnlyWhenARetryHappened(t *testing.T) {
	retryMu.Lock()
	saved := retryLog
	retryLog = nil
	retryMu.Unlock()
	t.Cleanup(func() {
		retryMu.Lock()
		retryLog = saved
		retryMu.Unlock()
		log.SetOutput(os.Stderr)
	})

	var buf bytes.Buffer
	log.SetOutput(&buf)
	Summary()
	if buf.Len() != 0 {
		t.Fatalf("summary printed %q with no retries", buf.String())
	}

	noteRetry("fixture", errors.New("timed out"))
	buf.Reset()
	Summary()
	if out := buf.String(); !strings.Contains(out, "1 dial retries in this package") {
		t.Fatalf("summary = %q, want one count line", out)
	}
}

// RetryConnect retries only a failed TCP connect: that is the case the host
// causes. A failure after the connection existed must not be retried. Every
// connect retry is counted, because the package summary is the only place a
// passing run shows it.
func TestRetryConnectRetriesOnlyDialFailures(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("timed out")}
	var dials int
	before := RetryCount()
	got, err := RetryConnect(t, "fixture", func() (string, error) {
		dials++
		if dials == 1 {
			return "", dialErr
		}
		return "connected", nil
	})
	if err != nil || got != "connected" || dials != 2 {
		t.Fatalf("connect retry = %q, %v after %d dials, want success on the second", got, err, dials)
	}
	if n := RetryCount() - before; n != 1 {
		t.Fatalf("connect retry counted %d times, want 1", n)
	}

	other := errors.New("handshake rejected")
	dials = 0
	before = RetryCount()
	if _, err = RetryConnect(t, "fixture", func() (string, error) {
		dials++
		return "", other
	}); err == nil || dials != 1 {
		t.Fatalf("a non-dial failure was retried: err=%v dials=%d", err, dials)
	}
	if n := RetryCount() - before; n != 0 {
		t.Fatalf("a non-dial failure was counted as a retry %d times", n)
	}
}

// unreachableError is the shape a caller reports when the failure arrived as a
// value (a greeting frame) instead of as the original dial error.
type unreachableError struct{ msg string }

func (e *unreachableError) Error() string { return e.msg }

// A failure reported as a value rather than as a dial error still has to be
// retryable when the caller can recognise it: the terminal bridge sends its
// host's connect failure as a greeting frame, so the *net.OpError is gone.
// RetryConnectWhen is the seam for that, and it must keep the same limits.
func TestRetryConnectWhenUsesTheCallersTest(t *testing.T) {
	recognise := func(err error) bool {
		var u *unreachableError
		return errors.As(err, &u)
	}

	var runs int
	before := RetryCount()
	got, err := RetryConnectWhen(t, "bridge", func() (string, error) {
		runs++
		if runs == 1 {
			return "", &unreachableError{msg: "greeting carried a connect failure"}
		}
		return "hello", nil
	}, recognise)
	if err != nil || got != "hello" || runs != 2 {
		t.Fatalf("value-shaped retry = %q, %v after %d runs, want success on the second", got, err, runs)
	}
	if n := RetryCount() - before; n != 1 {
		t.Fatalf("value-shaped retry counted %d times, want 1", n)
	}

	// The caller's test decides: a failure it does not recognise is final, so a
	// genuinely broken open is never retried into a false pass.
	runs = 0
	before = RetryCount()
	if _, err = RetryConnectWhen(t, "bridge", func() (string, error) {
		runs++
		return "", errors.New("handshake rejected")
	}, recognise); err == nil || runs != 1 {
		t.Fatalf("an unrecognised failure was retried: err=%v runs=%d", err, runs)
	}
	if n := RetryCount() - before; n != 0 {
		t.Fatalf("an unrecognised failure was counted as a retry %d times", n)
	}
}
