package main

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// The cleanup budget must outlive the drain window. At 10s the process exited
// while DrainRunning was still waiting for executors to write down evidence.
func TestShutdownCleanupGraceExceedsDrainWindow(t *testing.T) {
	if shutdownCleanupGrace <= shutdownDrainTimeout {
		t.Fatalf("shutdownCleanupGrace=%s must exceed shutdownDrainTimeout=%s", shutdownCleanupGrace, shutdownDrainTimeout)
	}
}

func TestShutdownClosesListenerEvenWhenCleanupBlocks(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	blocked, done := make(chan struct{}), make(chan struct{})
	defer close(blocked)
	go func() { shutdownConsole(srv, 30*time.Millisecond, func() { <-blocked }); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup trapped shutdown")
	}
	select {
	case err := <-served:
		if err != http.ErrServerClosed {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener stayed open")
	}
}
