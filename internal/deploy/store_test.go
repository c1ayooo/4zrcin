package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A deployment outlives the page and the process: the kept jobs reload with
// their log, and a job the platform left running reads as interrupted, never
// as still running (docs/14 §7.4).
func TestJobsSurviveRestartAndRunningOnesReadInterrupted(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.DataDir = filepath.Join(t.TempDir(), "data") })
	srv := startSSHServer(t, sshServerOptions{password: "pw-keep", uid: "0"})
	srv.failBootstrap = true
	view, err := f.dep.Start(Options{Host: "127.0.0.1", Port: portOf(t, srv.addr), User: "root", Password: "pw-keep"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, f.dep, view.ID, 10*time.Second)
	if done.State != StateFailed {
		t.Fatalf("state = %s", done.State)
	}
	raw, err := os.ReadFile(f.dep.deployPath())
	if err != nil {
		t.Fatalf("the finished job was not written: %v", err)
	}
	if string(raw) == "" || !strings.Contains(string(raw), view.ID) {
		t.Fatal("the job file does not carry the job")
	}
	if strings.Contains(string(raw), "pw-keep") {
		t.Fatal("the job file carries the credential")
	}

	// The platform restarts: the same data dir, a new service.
	reloaded, err := New(Config{Enroll: f.svc, Dist: f.dist, DataDir: f.dep.cfg.DataDir, Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	jobs := reloaded.List(0)
	if len(jobs) != 1 || jobs[0].ID != view.ID || jobs[0].State != StateFailed {
		t.Fatalf("reloaded jobs = %+v", jobs)
	}
	if len(jobs[0].Log) == 0 {
		t.Fatal("the reloaded job lost its log")
	}

	// A job that was running when the process went down.
	j := &job{
		svc: reloaded, id: "j-running", enrollmentID: "e1", host: "10.0.0.9", port: 22, user: "root",
		state: StateRunning, createdAt: time.Now().Add(-time.Minute),
	}
	for _, st := range stepOrder {
		j.steps = append(j.steps, stepState{key: st.key, label: st.label, state: StepPending})
	}
	reloaded.mu.Lock()
	reloaded.jobs[j.id] = j
	reloaded.mu.Unlock()
	if err := reloaded.persist(); err != nil {
		t.Fatal(err)
	}
	again, err := New(Config{Enroll: f.svc, Dist: f.dist, DataDir: f.dep.cfg.DataDir, Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var got *JobView
	for _, v := range again.List(0) {
		if v.ID == "j-running" {
			v := v
			got = &v
		}
	}
	if got == nil || got.State != StateInterrupted || got.Error == "" || got.FinishedAt == nil {
		t.Fatalf("a job left running reloaded as %+v", got)
	}
}
