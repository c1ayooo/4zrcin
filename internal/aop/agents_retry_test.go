package aop

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// roundTripFunc lets a test answer an HTTP request without a socket.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func agentsJSON(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func withAgentsClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := agentsHTTPClient
	agentsHTTPClient = &http.Client{Transport: rt}
	t.Cleanup(func() { agentsHTTPClient = prev })
}

// The management call is the third path to the host, and a stalled dial there
// failed a round before one byte of the request was written (2026-11-22:
// `aop: 列出 agent 失败：Post … connectex …`). Only the connect phase repeats.
func TestAgentsRetriesADialTimeout(t *testing.T) {
	var calls int32
	withAgentsClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: syscall.Errno(10060)}}
		}
		return agentsJSON(`{"agents":[{"hello":{"nodeId":"n1","name":"local"}}]}`), nil
	}))

	agents, err := New("http://127.0.0.1:18799", "").Agents(context.Background())
	if err != nil {
		t.Fatalf("a dial timeout on ListAgents was not retried: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "n1" {
		t.Fatalf("agents = %+v, want the retry's answer", agents)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("requests = %d, want 2 (one retry after the dial timeout)", got)
	}
}

// A refusal is final; so is a read timeout *after* the request went out: the
// host may have acted on it, and repeating a POST would then be a duplicate.
func TestAgentsDoesNotRetryAfterTheRequestWasWritten(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"refused dial", &url.Error{Op: "Post", URL: "http://x/", Err: &net.OpError{
			Op: "dial", Net: "tcp", Err: syscall.Errno(10061)}}},
		{"read timeout", &url.Error{Op: "Post", URL: "http://x/", Err: &net.OpError{
			Op: "read", Net: "tcp", Err: syscall.Errno(10060)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			withAgentsClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return nil, tc.err
			}))
			if _, err := New("http://127.0.0.1:18799", "").Agents(context.Background()); err == nil {
				t.Fatal("a failed ListAgents was reported as success")
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Fatalf("requests = %d, want 1 (%s is not a connect timeout)", got, tc.name)
			}
		})
	}
}

// An answered request is never repeated, whatever the status: the host has
// already made a statement.
func TestAgentsDoesNotRetryAnAnsweredRequest(t *testing.T) {
	var calls int32
	withAgentsClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{StatusCode: http.StatusInternalServerError,
			Body: io.NopCloser(strings.NewReader("boom"))}, nil
	}))
	if _, err := New("http://127.0.0.1:18799", "").Agents(context.Background()); err == nil {
		t.Fatal("HTTP 500 was reported as success")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("requests = %d, want 1 (an answer is final)", got)
	}
}
