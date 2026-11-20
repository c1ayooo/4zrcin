package testutil

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
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

// A dial that fails before a connection exists is retried exactly once, and the
// retry is recorded so a report can show it.
func TestDialRetryRetriesOnceAndRecordsIt(t *testing.T) {
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
	events := retrySnapshot()[before:]
	if len(events) == 0 {
		t.Fatal("a refused dial was not retried")
	}
	if len(events) > maxDialAttempts-1 {
		t.Fatalf("retried %d times, want at most %d", len(events), maxDialAttempts-1)
	}
	if !strings.Contains(events[0].addr, addr) {
		t.Fatalf("retry recorded for %q, want %q", events[0].addr, addr)
	}
}

// RetryConnect retries only a failed TCP connect: that is the case the host
// causes. A failure after the connection existed must not be retried.
func TestRetryConnectRetriesOnlyDialFailures(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("timed out")}
	var dials int
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

	other := errors.New("handshake rejected")
	dials = 0
	if _, err = RetryConnect(t, "fixture", func() (string, error) {
		dials++
		return "", other
	}); err == nil || dials != 1 {
		t.Fatalf("a non-dial failure was retried: err=%v dials=%d", err, dials)
	}
}
