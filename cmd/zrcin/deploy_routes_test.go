package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/deploy"
	"github.com/c1ayooo/4zrcin/internal/enroll"
)

// SSH deployment is an operator route next to the enrollment surface
// (docs/14 §7.1, §7.2): off with the rest of the ingress, one POST to start a
// job, one GET to read its steps and log.

func newDeployTestHub(t *testing.T) *hub {
	t.Helper()
	h := newEnrollTestHub(t, true)
	launcher := filepath.Join(t.TempDir(), "zrcin-node")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dist := enroll.NewDist(enroll.DistConfig{
		DataDir: filepath.Join(t.TempDir(), "dist"),
		Paths:   map[string]string{enroll.ArtLauncher: launcher},
	})
	dep, err := deploy.New(deploy.Config{
		Enroll: h.cfg.RemoteNodes,
		Dist:   dist,
		Wait:   150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Deploys = dep
	return h
}

func TestNodeDeploysOffAnswers404(t *testing.T) {
	h := newEnrollTestHub(t, false)
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/api/node-deploys"},
		{http.MethodGet, "/api/node-deploys/x"},
	} {
		rec := doHub(t, h, probe.method, probe.path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404", probe.method, probe.path, rec.Code)
		}
		if probe.method == http.MethodPost && !strings.Contains(rec.Body.String(), "--public-url") {
			t.Fatalf("disabled hint = %q", rec.Body.String())
		}
	}
}

func TestNodeDeployJobLifecycle(t *testing.T) {
	h := newDeployTestHub(t)

	// Bad JSON and bad options are rejected before any record is created.
	if rec := doHub(t, h, http.MethodPost, "/api/node-deploys", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d %s", rec.Code, rec.Body)
	}
	if rec := doHub(t, h, http.MethodPost, "/api/node-deploys", `{"host":"127.0.0.1","user":"root"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing auth = %d %s", rec.Code, rec.Body)
	}
	if got := len(h.cfg.RemoteNodes.ListEnrollments()); got != 0 {
		t.Fatalf("validation failures left %d enrollment(s)", got)
	}

	// A valid request runs: the host here refuses the connection, so the job
	// must end failed with the connect step marked, and the API must expose
	// exactly that.
	rec := doHub(t, h, http.MethodPost, "/api/node-deploys",
		`{"host":"127.0.0.1","port":1,"user":"root","password":"pw","name":"edge-ssh"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	var view deploy.JobView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("start body: %v", err)
	}
	if view.ID == "" || view.EnrollmentID == "" || view.State != deploy.StateRunning {
		t.Fatalf("start view = %+v", view)
	}
	if strings.Contains(rec.Body.String(), `"password"`) || strings.Contains(rec.Body.String(), `"private_key"`) {
		t.Fatal("the job view echoes a credential field")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec = doHub(t, h, http.MethodGet, "/api/node-deploys/"+view.ID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get = %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatalf("get body: %v", err)
		}
		if view.State != deploy.StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job stayed running: %+v", view)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if view.State != deploy.StateFailed || view.Error == "" {
		t.Fatalf("finished view = %+v", view)
	}
	failed := false
	for _, step := range view.Steps {
		if step.Key == "connect" && step.State == deploy.StepFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("connect step = %+v", view.Steps)
	}
	// The page restores what is deploying from the list: newest first, the
	// job that just ended included.
	rec = doHub(t, h, http.MethodGet, "/api/node-deploys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Jobs []deploy.JobView `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("list body: %v", err)
	}
	if len(list.Jobs) != 1 || list.Jobs[0].ID != view.ID {
		t.Fatalf("list = %+v, want the one job", list.Jobs)
	}
	if strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatal("the list echoes a credential field")
	}
	if rec := doHub(t, h, http.MethodGet, "/api/node-deploys/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown job = %d", rec.Code)
	}
	if rec := doHub(t, h, http.MethodPost, "/api/node-deploys/"+view.ID, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("item POST = %d", rec.Code)
	}
}
