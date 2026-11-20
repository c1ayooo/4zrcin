// Package testutil holds test-only HTTP plumbing.
//
// The host this project is developed on occasionally drops loopback SYNs:
// connect() to 127.0.0.1 reports a ~1s WSAETIMEDOUT even though the listener is
// healthy (docs/01-architecture.md §9). Any test that only needs an HTTP
// contract — method, path, headers, body, status — therefore does not need a
// TCP connection at all: it can route the request straight into the handler.
//
// This package is imported from _test.go files only; it is not part of any
// binary. It never decides what a test asserts.
package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	hosts       sync.Map // host name -> http.Handler
	hostSeq     atomic.Int64
	installOnce sync.Once
)

// init installs on import. Every package that imports this one is a test
// binary, and the install is idempotent, so no package needs its own TestMain
// just to opt in.
func init() { Install() }

// Install prepares http.DefaultTransport for the two mechanisms this package
// provides: requests for hosts registered by NewServer are dispatched straight
// into their handler, and any remaining real dial gets the narrow dial-level
// retry described in dialretry.go. Call it once from TestMain, before any test
// builds a client.
func Install() {
	installOnce.Do(func() {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			// Something already replaced the default transport; do not guess
			// how to clone it.
			return
		}
		tr := base.Clone()
		tr.DialContext = newRetryDialContext(tr.DialContext)
		http.DefaultTransport = inProcessTransport{base: tr}
	})
}

// NewServer registers h under a unique host name and returns an
// *httptest.Server whose URL addresses it. No listener is opened: the return
// value is a handle only, so its URL works exactly like httptest.NewServer's
// for callers that build requests from it.
//
// Install must have been called first (normally once from TestMain). Close is
// safe and idempotent on an unstarted server.
func NewServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("inproc-%d.test", hostSeq.Add(1))
	hosts.Store(name, h)
	t.Cleanup(func() { hosts.Delete(name) })
	srv := httptest.NewUnstartedServer(h)
	srv.URL = "http://" + name
	return srv
}

// NewServerFunc is NewServer for a plain handler function.
func NewServerFunc(t *testing.T, h func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return NewServer(t, http.HandlerFunc(h))
}

// Client returns an http.Client that dispatches every request into h
// in-process, for tests that do not need a base URL at all.
func Client(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	name := fmt.Sprintf("inproc-%d.test", hostSeq.Add(1))
	hosts.Store(name, h)
	t.Cleanup(func() { hosts.Delete(name) })
	return &http.Client{Transport: inProcessTransport{base: http.DefaultTransport, only: name}}
}

type inProcessTransport struct {
	base http.RoundTripper
	only string // when set, only this host routes in-process
}

func (tr inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if tr.only != "" && host != tr.only {
		return tr.base.RoundTrip(req)
	}
	if h, ok := hosts.Load(host); ok {
		rec := httptest.NewRecorder()
		h.(http.Handler).ServeHTTP(rec, req)
		return rec.Result(), nil
	}
	return tr.base.RoundTrip(req)
}
