package project

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestManifestSettersRestoreStateOnWriteFailure(t *testing.T) {
	for _, stage := range []string{"open", "rename"} {
		for _, field := range []string{"scope", "description", "max_runs", "verify_auto", "round_timeout", "nodes", "peer_policy"} {
			t.Run(stage+"/"+field, func(t *testing.T) {
				m, err := Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				p, err := m.Create("fixture", "original scope")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = p.Store.Close() })
				if _, err := p.SetPeerPolicy(PeerPolicy{Mode: PeerPolicyAllowlist}); err != nil {
					t.Fatal(err)
				}
				before := p.Manifest
				path := filepath.Join(p.Dir, ManifestFile)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				blocker := path + ".tmp"
				if stage == "rename" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					blocker = path
				}
				// A directory fails consistently on Windows and Unix, including when
				// the test is run by a user that can bypass file permission bits.
				if err := os.Mkdir(blocker, 0o755); err != nil {
					t.Fatal(err)
				}
				switch field {
				case "scope":
					err = m.SetScope(p.ID, "new scope")
				case "description":
					err = m.SetDescription(p.ID, "new description")
				case "max_runs":
					limit := 7
					err = m.SetMaxRuns(p.ID, &limit)
				case "verify_auto":
					auto := true
					err = m.SetVerifyAuto(p.ID, &auto)
				case "round_timeout":
					err = m.SetRoundTimeout(p.ID, "1m")
				case "nodes":
					err = p.SetNodeBinding([]string{"local"}, "local")
				case "peer_policy":
					_, err = p.SetPeerPolicy(PeerPolicy{Mode: PeerPolicyOpen})
				}
				if err == nil {
					t.Fatal("blocked manifest write succeeded")
				}
				if !reflect.DeepEqual(p.Manifest, before) {
					t.Errorf("failed write changed the live manifest: got %+v, want %+v", p.Manifest, before)
				}
				if p.PeerAllowed("pt_a", "pt_b") {
					t.Error("failed write widened communication permissions")
				}
				if err := os.Remove(blocker); err != nil {
					t.Fatal(err)
				}
				if stage == "rename" {
					if err := os.WriteFile(path, raw, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				// A later successful save must not commit a previously rejected edit.
				if err := p.SetPaused(p.IsPaused()); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, after) {
					t.Error("a later save persisted the failed edit")
				}
			})
		}
	}
}

func TestFailedManifestWritesLeaveImmutableIdentityUntouched(t *testing.T) {
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Create("fixture", "scope")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Store.Close() })
	if err := os.Mkdir(filepath.Join(p.Dir, ManifestFile)+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	id, name, created := p.ID, p.Name, p.Created
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 1000; i++ {
			// The hub and dispatchers read these immutable identity fields without
			// p.mu. A rollback may only restore mutable settings, not the struct.
			if p.ID != id || p.Name != name || p.Created != created {
				t.Error("failed settings save changed project identity")
			}
		}
	})
	for i := 0; i < 100; i++ {
		if err := m.SetScope(id, "rejected"); err == nil {
			t.Error("blocked write succeeded")
		}
	}
	wg.Wait()
}
