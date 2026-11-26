package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/model"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// sqliteMeta is the single meta-row payload: the ledger schema version, the
// message read cursor's high-water mark and the copy-on-write Memory. The name
// is historical; both dialects store the same shape.
type sqliteMeta struct {
	Version    int     `json:"version"`
	MessageSeq uint64  `json:"message_seq"`
	Memory     *Memory `json:"memory"`
}

// dbBackend is the SQL handle behind the store's flush and indexed reads.
// The store's domain logic is dialect-agnostic; everything that differs
// between SQLite and PostgreSQL lives here: placeholder syntax, the physical
// format marker, fact_search (FTS5 vs pg_trgm) and the snapshot export.
// M0 keeps the in-memory model; PostgreSQL is a persistence engine of the
// same shape as the SQLite ledger (docs/16 §13 M0).
type dbBackend struct {
	db *sql.DB
	pg bool
}

// dialect rewrites positional '?' markers for PostgreSQL. Statements owned by
// this package never contain '?' inside a string literal; quote toggling is
// tracked anyway so a future literal stays safe.
func (b *dbBackend) dialect(q string) string {
	if !b.pg {
		return q
	}
	return pgPlaceholders(q)
}

func (b *dbBackend) Exec(q string, args ...any) (sql.Result, error) {
	return b.db.Exec(b.dialect(q), args...)
}

func (b *dbBackend) Query(q string, args ...any) (*sql.Rows, error) {
	return b.db.Query(b.dialect(q), args...)
}

func (b *dbBackend) QueryRow(q string, args ...any) *sql.Row {
	return b.db.QueryRow(b.dialect(q), args...)
}

func (b *dbBackend) Begin() (*dbTx, error) {
	tx, err := b.db.Begin()
	if err != nil {
		return nil, err
	}
	return &dbTx{tx: tx, pg: b.pg}, nil
}

func (b *dbBackend) Close() error { return b.db.Close() }

type dbTx struct {
	tx *sql.Tx
	pg bool
}

func (t *dbTx) Exec(q string, args ...any) (sql.Result, error) {
	if t.pg {
		q = pgPlaceholders(q)
	}
	return t.tx.Exec(q, args...)
}

func (t *dbTx) Query(q string, args ...any) (*sql.Rows, error) {
	if t.pg {
		q = pgPlaceholders(q)
	}
	return t.tx.Query(q, args...)
}

func (t *dbTx) QueryRow(q string, args ...any) *sql.Row {
	if t.pg {
		q = pgPlaceholders(q)
	}
	return t.tx.QueryRow(q, args...)
}

func (t *dbTx) Rollback() error { return t.tx.Rollback() }
func (t *dbTx) Commit() error   { return t.tx.Commit() }

func pgPlaceholders(q string) string {
	n := 0
	var sb strings.Builder
	inLiteral := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\'' {
			inLiteral = !inLiteral
		}
		if c == '?' && !inLiteral {
			n++
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(n))
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// pgFormatVersion is the physical layout of the PostgreSQL ledger, the
// counterpart of SQLite's PRAGMA user_version. The ledger schema itself is
// checked through the meta row, exactly like SQLite.
const pgFormatVersion = 1

var pgDDL = []string{
	`CREATE TABLE IF NOT EXISTS ledger_format (id INTEGER PRIMARY KEY, v INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS meta (id INTEGER PRIMARY KEY CHECK(id=1), payload BYTEA NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS objects (rowid BIGSERIAL, kind TEXT NOT NULL, id TEXT NOT NULL, position BIGINT NOT NULL, version BIGINT NOT NULL, seq BIGINT NOT NULL, payload BYTEA NOT NULL, PRIMARY KEY(kind,id))`,
	`CREATE INDEX IF NOT EXISTS objects_order ON objects(kind,position,id)`,
	`CREATE TABLE IF NOT EXISTS versions (seq BIGINT PRIMARY KEY, kind TEXT NOT NULL, id TEXT NOT NULL, version BIGINT NOT NULL, payload BYTEA NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS versions_object ON versions(kind,id,seq)`,
	`CREATE TABLE IF NOT EXISTS fact_search (object_rowid BIGINT PRIMARY KEY, id TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', doc TEXT NOT NULL DEFAULT '')`,
	`CREATE INDEX IF NOT EXISTS fact_search_title_trgm ON fact_search USING gin (title gin_trgm_ops)`,
	`CREATE INDEX IF NOT EXISTS fact_search_summary_trgm ON fact_search USING gin (summary gin_trgm_ops)`,
	`CREATE INDEX IF NOT EXISTS fact_search_doc_trgm ON fact_search USING gin (doc gin_trgm_ops)`,
	`CREATE TABLE IF NOT EXISTS vectors (seq BIGSERIAL PRIMARY KEY, model TEXT NOT NULL, hash TEXT NOT NULL, vector BYTEA NOT NULL, UNIQUE(model,hash))`,
	`CREATE TABLE IF NOT EXISTS relations (owner TEXT NOT NULL, source TEXT NOT NULL, target TEXT NOT NULL, relation TEXT NOT NULL, PRIMARY KEY(owner,source,target,relation))`,
	`CREATE INDEX IF NOT EXISTS relations_source ON relations(source)`,
	`CREATE INDEX IF NOT EXISTS relations_target ON relations(target)`,
}

// OpenPG opens a ledger stored in PostgreSQL. The DSN is a pgx connection
// string; a search_path in the options parameter scopes the ledger to one
// schema, which is how tests isolate stores on a shared database. The
// database (or schema) must have the pg_trgm extension available.
func OpenPG(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// The store serializes writes under its own mutex; a small pool lets
	// indexed reads (change window, neighborhood, vectors) run concurrently.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	fail := func(e error) (*Store, error) { _ = db.Close(); return nil, e }
	b := &dbBackend{db: db, pg: true}
	for _, stmt := range pgDDL {
		if _, err = b.Exec(stmt); err != nil {
			return fail(err)
		}
	}
	var format int
	err = b.QueryRow("SELECT v FROM ledger_format WHERE id=1").Scan(&format)
	if err == sql.ErrNoRows {
		mem := &Memory{Epoch: NewID("ledger"), Checkpoints: map[string]Checkpoint{}}
		mem.ContentHash = contentHash(nil)
		raw, _ := json.Marshal(sqliteMeta{schemaVersion, 0, mem})
		if _, err = b.Exec("INSERT INTO meta(id,payload) VALUES(1,?) ON CONFLICT(id) DO NOTHING", raw); err != nil {
			return fail(err)
		}
		if _, err = b.Exec("INSERT INTO ledger_format(id,v) VALUES(1,?) ON CONFLICT(id) DO NOTHING", pgFormatVersion); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	} else if format != pgFormatVersion {
		return fail(fmt.Errorf("unsupported PostgreSQL ledger format %d", format))
	}
	s := &Store{path: dsn, be: b}
	if err = s.loadSQLite(); err != nil {
		return fail(err)
	}
	return s, nil
}

// pgFullTextCandidates mirrors the SQLite trigram path: every term must match
// somewhere in title, summary or doc, and terms shorter than three runes fall
// back to the lexical path (not handled). The score is the sum of trigram
// similarities across terms: higher is closer. bm25 ordering differs, which
// docs/17 C10 accepts for candidate generation.
func pgFullTextCandidates(b *dbBackend, q string) (map[string]float64, bool, error) {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil, false, nil
	}
	conds := []string{}
	scoreExpr := []string{}
	for _, t := range terms {
		if len([]rune(t)) < 3 {
			return nil, false, nil
		}
		scoreExpr = append(scoreExpr, "greatest(similarity(title,?),similarity(summary,?),similarity(doc,?))")
		conds = append(conds, "(title % ? OR summary % ? OR doc % ?)")
	}
	query := "SELECT id, " + strings.Join(scoreExpr, "+") + " FROM fact_search WHERE " + strings.Join(conds, " AND ")
	args := []any{}
	for _, t := range terms {
		// Three placeholders in the score expression, three in the condition.
		args = append(args, t, t, t, t, t, t)
	}
	rows, err := b.Query(query, args...)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var score float64
		if err = rows.Scan(&id, &score); err != nil {
			return nil, true, err
		}
		out[id] = score
	}
	return out, true, rows.Err()
}

// pgBackupTo writes a standalone SQLite ledger holding exactly the committed
// rows of the PostgreSQL ledger, so the export is readable by ReadSnapshot,
// the offline tools and a desktop instance without this build. Called with
// the store's read lock held (see BackupTo).
func pgBackupTo(s *Store, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range sqliteDDL {
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	fail := func(e error) error { return fmt.Errorf("snapshot PostgreSQL ledger: %w", e) }
	var raw []byte
	if err = s.be.QueryRow("SELECT payload FROM meta WHERE id=1").Scan(&raw); err != nil {
		return fail(err)
	}
	if _, err = tx.Exec("INSERT INTO meta VALUES(1,?)", raw); err != nil {
		return fail(err)
	}
	rows, err := s.be.Query("SELECT kind,id,position,version,seq,payload FROM objects ORDER BY rowid")
	if err != nil {
		return fail(err)
	}
	type objRow struct {
		kind                   string
		id                     string
		position, version, seq uint64
		payload                []byte
	}
	objects := []objRow{}
	for rows.Next() {
		var r objRow
		var position, version, seq int64
		if err = rows.Scan(&r.kind, &r.id, &position, &version, &seq, &r.payload); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		r.position, r.version, r.seq = uint64(position), uint64(version), uint64(seq)
		objects = append(objects, r)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return fail(err)
	}
	rows.Close()
	for _, r := range objects {
		if _, err = tx.Exec("INSERT INTO objects(kind,id,position,version,seq,payload) VALUES(?,?,?,?,?,?)",
			r.kind, r.id, r.position, r.version, r.seq, r.payload); err != nil {
			return fail(err)
		}
		if r.kind == "fact" {
			var rowID int64
			if err = tx.QueryRow("SELECT rowid FROM objects WHERE kind='fact' AND id=?", r.id).Scan(&rowID); err != nil {
				return fail(err)
			}
			var f model.Fact
			if err = json.Unmarshal(r.payload, &f); err != nil {
				return fail(err)
			}
			if _, err = tx.Exec("INSERT INTO fact_search(rowid,id,title,summary,doc) VALUES(?,?,?,?,?)",
				rowID, f.ID, f.Title, f.Summary, f.Doc); err != nil {
				return fail(err)
			}
		}
	}
	rows, err = s.be.Query("SELECT seq,kind,id,version,payload FROM versions ORDER BY seq")
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var seq, version int64
		var kind, id string
		var payload []byte
		if err = rows.Scan(&seq, &kind, &id, &version, &payload); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		if _, err = tx.Exec("INSERT INTO versions(seq,kind,id,version,payload) VALUES(?,?,?,?,?)", seq, kind, id, version, payload); err != nil {
			_ = rows.Close()
			return fail(err)
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return fail(err)
	}
	rows.Close()
	rows, err = s.be.Query("SELECT owner,source,target,relation FROM relations")
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var owner, source, target, relation string
		if err = rows.Scan(&owner, &source, &target, &relation); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO relations VALUES(?,?,?,?)", owner, source, target, relation); err != nil {
			_ = rows.Close()
			return fail(err)
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return fail(err)
	}
	rows.Close()
	rows, err = s.be.Query("SELECT model,hash,vector FROM vectors ORDER BY seq")
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var model, hash string
		var vector []byte
		if err = rows.Scan(&model, &hash, &vector); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		if _, err = tx.Exec("INSERT INTO vectors(model,hash,vector) VALUES(?,?,?)", model, hash, vector); err != nil {
			_ = rows.Close()
			return fail(err)
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return fail(err)
	}
	rows.Close()
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	return os.Chmod(path, 0o600)
}
