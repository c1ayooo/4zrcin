package project

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// Server form: the project directory carries working files only, the ledger
// lives in one PostgreSQL schema per project id (docs/16 §9.2). The roundtrip
// is the whole point: create → write → reopen the manager → the rows are
// there; drop → the schema is gone.
func TestPGLedgerManagerRoundTrip(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	root := t.TempDir()
	mgr, err := OpenPooledDSN(root, dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("服务器形态回归", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.Store.AddFact(model.Fact{IntentID: "in_1", Kind: "vuln", Title: "跨重启的事实"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.LedgerPath()); !os.IsNotExist(err) {
		t.Fatalf("server form must not materialize a ledger file: %v", err)
	}

	// A fresh manager over the same root must load the engagement from the
	// schema — no ledger file, no loss.
	mgr2, err := OpenPooledDSN(root, dsn)
	if err != nil {
		t.Fatal(err)
	}
	got := mgr2.Get(p.ID)
	if got == nil {
		t.Fatal("reopened manager lost the project")
	}
	row, ok := got.Store.FactByID(f.ID)
	if !ok || row.Title != "跨重启的事实" {
		t.Fatalf("ledger rows lost across manager reopen: %+v %v", row, ok)
	}
	if err := got.Store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := mgr2.DropLedger(p.ID); err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var n int
	if err := admin.QueryRow("SELECT count(*) FROM pg_namespace WHERE nspname=$1", "ledger_"+p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("DropLedger left the schema behind")
	}
}
