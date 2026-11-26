package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// Test fixtures shared by both dialects.
//
// open(t) opens a SQLite ledger by default; setting ZRCIN_TEST_PG_DSN switches
// the whole suite onto PostgreSQL, one throwaway schema per test. Without the
// variable no test needs a database server, so the desktop development loop
// stays dependency-free (docs/17 §3).

// pgAdminConn keeps one administrative connection open for the whole test:
// the schema cleanup at the end still needs it.
func pgAdminConn(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return admin
}

// pgSchemaDSN scopes a DSN to a schema; public stays on the search path so
// the pg_trgm operators resolve.
func pgSchemaDSN(dsn, schema string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema + ",public"
}

func createSchema(t *testing.T, admin *sql.DB, dsn, schema string) {
	t.Helper()
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
}

// openPGSchema creates a throwaway schema on the shared test database and
// opens a ledger scoped to it.
func openPGSchema(t *testing.T, dsn string) *Store {
	t.Helper()
	admin := pgAdminConn(t, dsn)
	schema := "t_" + strings.ToLower(NewCode(10))
	createSchema(t, admin, dsn, schema)
	s, err := OpenPG(pgSchemaDSN(dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// pgPathSchemas maps a test-provided ledger path to the schema standing in
// for it, so an open-then-reopen sequence lands on the same schema. Tests in
// this package never run in parallel.
var pgPathSchemas = map[string]string{}

// openLedgerAt opens a ledger at a test path. On PostgreSQL the path stands
// in for a schema: the first open creates it, later opens on the same path
// reuse it.
func openLedgerAt(t *testing.T, path string) *Store {
	t.Helper()
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	schema, ok := pgPathSchemas[path]
	if !ok {
		admin := pgAdminConn(t, dsn)
		schema = "t_" + strings.ToLower(NewCode(10))
		createSchema(t, admin, dsn, schema)
		pgPathSchemas[path] = schema
	}
	s, err := OpenPG(pgSchemaDSN(dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// reopen opens the same ledger again, on the dialect it was created with.
// The old store stays open on purpose: SQLite and PostgreSQL both allow the
// second handle, and the caller asserts on what the fresh open sees.
func reopen(t *testing.T, s *Store) *Store {
	t.Helper()
	var opened *Store
	var err error
	if s.be.pg {
		opened, err = OpenPG(s.path)
	} else {
		opened, err = Open(s.path)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

// abortMetaUpdates installs a trigger that aborts any UPDATE of the meta row —
// the same fixture point in both dialects. SQLite uses RAISE(ABORT);
// PostgreSQL raises an exception from a plpgsql function. Both fail the
// running transaction, which is what the rollback tests inject.
func abortMetaUpdates(t *testing.T, s *Store, name, phase, message string) {
	t.Helper()
	if s.be.pg {
		fn := name + "_fn"
		body := "CREATE FUNCTION " + fn + "() RETURNS trigger AS $tx$ BEGIN RAISE EXCEPTION '" + message + "'; END $tx$ LANGUAGE plpgsql"
		if _, err := s.be.db.Exec(body); err != nil {
			t.Fatal(err)
		}
		if _, err := s.be.db.Exec("CREATE TRIGGER " + name + " " + phase + " UPDATE ON meta FOR EACH STATEMENT EXECUTE FUNCTION " + fn + "()"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := s.be.db.Exec("CREATE TRIGGER " + name + " " + phase + " UPDATE ON meta BEGIN SELECT RAISE(ABORT,'" + message + "'); END"); err != nil {
		t.Fatal(err)
	}
}

// abortInsertsOn aborts INSERTs of one kind on the objects table, the fixture
// behind the notebook commit-failure test.
func abortInsertsOn(t *testing.T, s *Store, kind, name, message string) {
	t.Helper()
	if s.be.pg {
		fn := name + "_fn"
		body := "CREATE FUNCTION " + fn + "() RETURNS trigger AS $tx$ BEGIN IF NEW.kind='" + kind + "' THEN RAISE EXCEPTION '" + message + "'; END IF; RETURN NEW; END $tx$ LANGUAGE plpgsql"
		if _, err := s.be.db.Exec(body); err != nil {
			t.Fatal(err)
		}
		if _, err := s.be.db.Exec("CREATE TRIGGER " + name + " BEFORE INSERT ON objects FOR EACH ROW EXECUTE FUNCTION " + fn + "()"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := s.be.db.Exec("CREATE TRIGGER " + name + " BEFORE INSERT ON objects WHEN NEW.kind='" + kind + "' BEGIN SELECT RAISE(ABORT,'" + message + "'); END"); err != nil {
		t.Fatal(err)
	}
}

func dropTriggerNamed(t *testing.T, s *Store, name string) {
	t.Helper()
	if s.be.pg {
		if _, err := s.be.db.Exec("DROP TRIGGER " + name + " ON meta"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.be.db.Exec("DROP FUNCTION " + name + "_fn()"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := s.be.db.Exec("DROP TRIGGER " + name); err != nil {
		t.Fatal(err)
	}
}

func dropObjectTriggerNamed(t *testing.T, s *Store, name string) {
	t.Helper()
	if s.be.pg {
		if _, err := s.be.db.Exec("DROP TRIGGER " + name + " ON objects"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.be.db.Exec("DROP FUNCTION " + name + "_fn()"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := s.be.db.Exec("DROP TRIGGER " + name); err != nil {
		t.Fatal(err)
	}
}

// readMetaVersion / writeMetaVersion replace the SQLite-only json_set and
// json_extract probes with portable Go: the schema-rejection tests matter on
// both dialects (docs/17 C12).
func readMetaVersion(t *testing.T, s *Store) int {
	t.Helper()
	var raw []byte
	if err := s.be.QueryRow("SELECT payload FROM meta WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var meta sqliteMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	return meta.Version
}

func writeMetaVersion(t *testing.T, s *Store, version int) {
	t.Helper()
	var raw []byte
	if err := s.be.QueryRow("SELECT payload FROM meta WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var meta sqliteMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta.Version = version
	raw, _ = json.Marshal(meta)
	if _, err := s.be.Exec("UPDATE meta SET payload=? WHERE id=1", raw); err != nil {
		t.Fatal(err)
	}
}

// reopenExpectError opens the same ledger again and returns the open error,
// for the schema-rejection tests: a refused open yields no store to clean up.
func reopenExpectError(t *testing.T, s *Store) error {
	t.Helper()
	if s.be.pg {
		_, err := OpenPG(s.path)
		return err
	}
	_, err := Open(s.path)
	return err
}

// The PostgreSQL ledger must export a standalone SQLite snapshot that the
// offline readers accept (docs/17 C11's transport half; the content roundtrip
// through .twproj is bundle's test).
func TestPGLedgerExportsAStandaloneSQLiteSnapshot(t *testing.T) {
	if os.Getenv("ZRCIN_TEST_PG_DSN") == "" {
		t.Skip("PostgreSQL snapshot export is exercised with ZRCIN_TEST_PG_DSN")
	}
	s := open(t)
	f, err := s.AddFact(model.Fact{IntentID: "in_1", Kind: "vuln", Title: "导出的结论", Summary: "快照可读", Doc: "evidence attached"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvidence(model.Evidence{URI: "in_1/proof.txt", IntentID: "in_1"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "copy.sqlite")
	if err := s.BackupTo(backup); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	db, err := ReadSnapshot(backup)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if db.Version != CurrentSchemaVersion() || len(db.Facts) != 1 || db.Facts[0].ID != f.ID {
		t.Fatalf("snapshot does not carry the committed fact: version=%d facts=%d", db.Version, len(db.Facts))
	}
	if len(db.Evidence) != 1 {
		t.Fatalf("snapshot evidence = %d, want 1", len(db.Evidence))
	}
	if len(db.Memory.History) == 0 {
		t.Fatal("snapshot lost the version history")
	}
	// The live ledger keeps working after the export.
	if _, err := s.AddFact(model.Fact{IntentID: "in_1", Kind: "note", Title: "after"}); err != nil {
		t.Fatal(err)
	}
}
