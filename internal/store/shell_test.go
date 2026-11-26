package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/model"
)

func sealedShell(url string) model.Shell {
	return model.Shell{URL: url, Type: model.ShellTypePHP, Protocol: model.ShellProtocolV2,
		Secret: "sealed", Carrier: "X-abcdef0123", Source: model.ShellSourceOperator,
		Status: model.ShellStatusUnknown}
}

// Shells are durable project state: a restart keeps them, the sealed
// credential included, and both lookups resolve.
func TestShellsSurviveRestartAndListInRegistrationOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seed := []string{"http://a.example/shell.php", "http://b.example/s.jsp", "http://c.example/x.aspx"}
	ids := map[string]string{}
	for i, url := range seed {
		row := sealedShell(url)
		row.Name = "shell " + url
		row.NodeID = "node-1"
		if i == 1 {
			checked := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
			row.LastChecked = &checked
		}
		created, err := s.CreateShell(row)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(created.ID, "sh_") || created.CreatedAt.IsZero() {
			t.Fatalf("created row = %+v", created)
		}
		ids[url] = created.ID
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	rows := reopened.ListShells()
	if len(rows) != len(seed) {
		t.Fatalf("rows = %d, want %d", len(rows), len(seed))
	}
	for i, url := range seed {
		if rows[i].URL != url || rows[i].ID != ids[url] {
			t.Fatalf("row %d = %s %q, want %s %q", i, rows[i].ID, rows[i].URL, ids[url], url)
		}
	}
	got, ok := reopened.GetShell(ids[seed[1]])
	if !ok || got.Secret != "sealed" || got.Carrier != "X-abcdef0123" || got.NodeID != "node-1" {
		t.Fatalf("reload lost the row: %+v", got)
	}
	if got.LastChecked == nil || !got.LastChecked.Equal(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("last check did not survive the reload: %+v", got.LastChecked)
	}
	if _, ok := reopened.GetShell("sh_missing"); ok {
		t.Fatal("an unknown id resolved to a row")
	}
}

// A row the store cannot interpret honestly must be refused before it lands:
// a v2 shell without a sealed key could never be operated, and a status
// outside the closed set would later read as a liveness decision.
func TestShellValidationRejectsImpossibleRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := sealedShell("http://a.example/shell.php")
	cases := []model.Shell{}
	mutate := func(fn func(*model.Shell)) {
		row := base
		fn(&row)
		cases = append(cases, row)
	}
	mutate(func(sh *model.Shell) { sh.URL = "ftp://a.example/shell.php" })
	mutate(func(sh *model.Shell) { sh.URL = "/shell.php" })
	mutate(func(sh *model.Shell) { sh.URL = "http://" })
	mutate(func(sh *model.Shell) { sh.URL = "http://a.example/" + strings.Repeat("a", 2100) })
	mutate(func(sh *model.Shell) { sh.Type = "python" })
	mutate(func(sh *model.Shell) { sh.Protocol = "v3" })
	mutate(func(sh *model.Shell) { sh.Secret = "" })
	mutate(func(sh *model.Shell) { sh.Carrier = "" })
	mutate(func(sh *model.Shell) { sh.Protocol = model.ShellProtocolLegacy; sh.Carrier = ""; sh.Secret = "" })
	mutate(func(sh *model.Shell) { sh.Source = "guess" })
	mutate(func(sh *model.Shell) { sh.Status = "maybe" })
	mutate(func(sh *model.Shell) { sh.LastOutput = strings.Repeat("x", model.MaxShellOutput+1) })
	for i, sh := range cases {
		if _, err := s.CreateShell(sh); err == nil {
			t.Fatalf("case %d: impossible shell accepted: %+v", i, sh)
		}
	}
	if len(s.ListShells()) != 0 {
		t.Fatalf("rejected rows landed anyway: %d", len(s.ListShells()))
	}
}

// One address serves one shell while it is live; a removed shell frees its
// address for a new deployment with a new key.
func TestShellAddressIsIdentityWhileLive(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first, err := s.CreateShell(sealedShell("http://a.example/shell.php"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateShell(sealedShell("http://a.example/shell.php"))
	if !errors.Is(err, ErrShellDuplicate) || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("second live row for one address = %v", err)
	}
	if len(s.ListShells()) != 1 {
		t.Fatalf("the rejected row landed anyway: %d", len(s.ListShells()))
	}

	if _, err := s.UpdateShell(first.ID, func(sh *model.Shell) { sh.Status = model.ShellStatusRemoved }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateShell(sealedShell("http://a.example/shell.php")); err != nil {
		t.Fatalf("a removed address did not free up: %v", err)
	}
	if len(s.ListShells()) != 2 {
		t.Fatalf("rows = %d, want the removed one and the new one", len(s.ListShells()))
	}
}

func TestUpdateShellGuardsIdentityAndRevalidates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created, err := s.CreateShell(sealedShell("http://a.example/shell.php"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateShell("sh_missing", func(*model.Shell) {}); !errors.Is(err, ErrShellMissing) {
		t.Fatalf("unknown id = %v", err)
	}

	for name, fn := range map[string]func(*model.Shell){
		"url":      func(sh *model.Shell) { sh.URL = "http://other.example/shell.php" },
		"type":     func(sh *model.Shell) { sh.Type = model.ShellTypeJSP },
		"protocol": func(sh *model.Shell) { sh.Protocol = model.ShellProtocolLegacy },
		"status":   func(sh *model.Shell) { sh.Status = "half-alive" },
	} {
		if _, err := s.UpdateShell(created.ID, fn); err == nil {
			t.Fatalf("changing %s was accepted", name)
		}
	}

	after, ok := s.GetShell(created.ID)
	if !ok || after != created {
		t.Fatalf("a refused mutation left its mark: %+v", after)
	}

	checked := time.Now()
	updated, err := s.UpdateShell(created.ID, func(sh *model.Shell) {
		sh.Status = model.ShellStatusAlive
		sh.OSKind = "linux"
		sh.LastOutput = "uid=33(www-data)"
		sh.NodeID = "node-2"
		sh.LastChecked = &checked
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.ShellStatusAlive || updated.OSKind != "linux" || updated.NodeID != "node-2" ||
		updated.LastOutput != "uid=33(www-data)" || updated.LastChecked == nil || !updated.LastChecked.Equal(checked) {
		t.Fatalf("update = %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatal("the update did not move the row's timestamp")
	}
}

// A shell write is bookkeeping: it must not move the revision the decider
// watches or the content digest the board is rebuilt from.
func TestShellWritesAreBookkeeping(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revision, digest := s.Revision(), s.ContentHash()

	created, err := s.CreateShell(sealedShell("http://a.example/shell.php"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateShell(created.ID, func(sh *model.Shell) { sh.Status = model.ShellStatusDead }); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteShell(created.ID); err != nil {
		t.Fatal(err)
	}
	if s.Revision() != revision || s.ContentHash() != digest {
		t.Fatalf("a shell write moved the revision (%d -> %d) or the digest", revision, s.Revision())
	}
}

// A returned row owns its state: a caller editing what it was handed must not
// reach the live ledger.
func TestShellSnapshotsAreDetached(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created, err := s.CreateShell(sealedShell("http://a.example/shell.php"))
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Now()
	if _, err := s.UpdateShell(created.ID, func(sh *model.Shell) { sh.LastChecked = &checked }); err != nil {
		t.Fatal(err)
	}

	fromList := s.ListShells()[0]
	fromGet, _ := s.GetShell(created.ID)
	fromView, _ := s.ReadView()
	*fromList.LastChecked = time.Time{}
	*fromGet.LastChecked = time.Time{}
	*fromView.Shells[0].LastChecked = time.Time{}

	live, _ := s.GetShell(created.ID)
	if live.LastChecked == nil || !live.LastChecked.Equal(checked) {
		t.Fatalf("a handed-out pointer reached the live row: %+v", live.LastChecked)
	}
}

func TestDeleteShellRemovesTheRowDurably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateShell(sealedShell("http://a.example/shell.php"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteShell(created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteShell(created.ID); !errors.Is(err, ErrShellMissing) {
		t.Fatalf("deleting twice = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if rows := reopened.ListShells(); len(rows) != 0 {
		t.Fatalf("a restart resurrected the row: %+v", rows)
	}
}
