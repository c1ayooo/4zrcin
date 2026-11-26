package deploy

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Deployments outlive the page that started them: a job is a long-running
// operation the operator walks away from, and losing it to a tab switch or a
// platform restart is how "what is deploying?" stopped being answerable. The
// records live beside the node profiles, atomically replaced, without a
// credential in them (docs/14 §7.4).
const (
	deployFileName = "node-deploys.json"
	// maxKeptJobs bounds the file: the newest jobs stay, the oldest go.
	maxKeptJobs = 50
	// persistDelay coalesces a burst of remote output into one write.
	persistDelay = 300 * time.Millisecond
)

type deployFile struct {
	Jobs []JobView `json:"jobs"`
}

// load reads the kept jobs and settles the ones the platform left running: a
// restart ends the SSH session with them, so they read as interrupted (the log
// and the reason stay, and the operator can retry), never as still running.
func (s *Service) load() error {
	if s.cfg.DataDir == "" {
		return nil
	}
	raw, err := os.ReadFile(s.deployPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file deployFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range file.Jobs {
		v := file.Jobs[i]
		j := &job{
			svc: s, id: v.ID, enrollmentID: v.EnrollmentID, host: v.Host, port: v.Port, user: v.User,
			state: v.State, errText: v.Error, os: v.OS, arch: v.Arch, nodeID: v.NodeID,
			createdAt: v.CreatedAt, finishedAt: v.FinishedAt, logs: append([]LogLine(nil), v.Log...),
		}
		for _, st := range v.Steps {
			j.steps = append(j.steps, stepState{key: st.Key, label: st.Label, state: st.State, detail: st.Detail})
		}
		if len(j.steps) == 0 {
			for _, st := range stepOrder {
				j.steps = append(j.steps, stepState{key: st.key, label: st.label, state: StepPending})
			}
		}
		if j.state == StateRunning {
			j.state = StateInterrupted
			j.errText = "平台在任务运行期间重启，连接已断开；日志保留，可重新发起"
			j.log(stepBootstrap, "任务被平台重启中断")
			now := s.cfg.Now().UTC()
			j.finishedAt = &now
		}
		s.jobs[j.id] = j
	}
	s.trimLocked()
	return nil
}

// deployPath is <data>/node-deploys.json.
func (s *Service) deployPath() string {
	return filepath.Join(s.cfg.DataDir, deployFileName)
}

// persist writes every job's current view. It is called with no job lock held
// (a job lock inside the map lock is the only order this package allows).
func (s *Service) persist() error {
	if s.cfg.DataDir == "" {
		return nil
	}
	s.mu.Lock()
	file := deployFile{Jobs: make([]JobView, 0, len(s.jobs))}
	for _, j := range s.jobs {
		file.Jobs = append(file.Jobs, j.view())
	}
	s.mu.Unlock()
	sort.Slice(file.Jobs, func(i, k int) bool { return file.Jobs[i].CreatedAt.After(file.Jobs[k].CreatedAt) })
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfg.DataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.cfg.DataDir, ".node-deploys-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) //nolint:errcheck // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.deployPath())
}

// persistSoon coalesces: a run of log lines becomes one write after a short
// delay, and finish() writes synchronously so a terminal state never waits.
func (s *Service) persistSoon() {
	if s.cfg.DataDir == "" {
		return
	}
	select {
	case s.saveWake <- struct{}{}:
	default:
	}
}

// persistLoop runs until the service is closed by the process ending; it is
// the only writer.
func (s *Service) persistLoop() {
	if s.cfg.DataDir == "" {
		return
	}
	for range s.saveWake {
		// Coalesce a burst: wait briefly, then write whatever accumulated.
		time.Sleep(persistDelay)
		// Drain further wakes so one burst is one write.
		for {
			select {
			case <-s.saveWake:
				continue
			default:
			}
			break
		}
		if err := s.persist(); err != nil {
			log.Printf("部署任务落盘：%v", err)
		}
	}
}

// trimLocked keeps the newest maxKeptJobs (running ones always stay).
func (s *Service) trimLocked() {
	if len(s.jobs) <= maxKeptJobs {
		return
	}
	type aged struct {
		id string
		at time.Time
	}
	var finished []aged
	for id, j := range s.jobs {
		// Under the map lock a job lock is the allowed order; the job's state
		// is written under its own lock, so read it through the view.
		v := j.view()
		if v.State == StateRunning {
			continue
		}
		finished = append(finished, aged{id: id, at: v.CreatedAt})
	}
	sort.Slice(finished, func(i, k int) bool { return finished[i].at.Before(finished[k].at) })
	for _, a := range finished {
		if len(s.jobs) <= maxKeptJobs {
			break
		}
		delete(s.jobs, a.id)
	}
}

// ErrJobRunning refuses to remove a job that is still running: the operator
// dismisses what has ended, not what the platform is doing.
var ErrJobRunning = errors.New("任务仍在运行，不能清除")

// Remove forgets one ended job (the operator dismissed it). A running job is
// refused: its record is what the page restores after a restart.
func (s *Service) Remove(id string) error {
	s.mu.Lock()
	j := s.jobs[id]
	if j == nil {
		s.mu.Unlock()
		return nil
	}
	running := j.view().State == StateRunning
	if !running {
		delete(s.jobs, id)
	}
	s.mu.Unlock()
	if running {
		return ErrJobRunning
	}
	return s.persist()
}

// List reports the newest jobs first, at most limit (0 = all kept).
func (s *Service) List(limit int) []JobView {
	s.mu.Lock()
	all := make([]*job, 0, len(s.jobs))
	for _, j := range s.jobs {
		all = append(all, j)
	}
	s.mu.Unlock()
	sort.Slice(all, func(i, k int) bool { return all[i].createdAt.After(all[k].createdAt) })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]JobView, 0, len(all))
	for _, j := range all {
		out = append(out, j.view())
	}
	return out
}
