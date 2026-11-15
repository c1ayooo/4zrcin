package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The edge cap is the backstop for handlers that decode straight from r.Body:
// an oversized body must fail before the handler can turn it into memory, and a
// normal body must pass through untouched.
func TestBodyLimitRejectsOversizedRequests(t *testing.T) {
	seen := ""
	h := withBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		seen = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("ok")))
	if res.Code != http.StatusOK || seen != "ok" {
		t.Fatalf("small body = %d, seen %q", res.Code, seen)
	}

	// Content-Length is present, so the middleware can answer without a read.
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader(strings.Repeat("x", maxRequestBody+1))))
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized declared body = %d, want 413", res.Code)
	}

	// No Content-Length (as with a chunked upload): the reader cap still stops
	// the handler once it crosses the limit.
	res = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader(strings.Repeat("x", maxRequestBody+1)))
	req.ContentLength = -1
	h.ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunked body = %d, want 413", res.Code)
	}
}
