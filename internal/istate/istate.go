// Package istate holds the instance-level state that server form keeps in
// PostgreSQL: one key/value table for the JSON documents (settings, notify
// configuration) and the SQL handle other instance stores reuse. Desktop form
// keeps the same documents as files under the data directory, unchanged.
package istate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DB is a *sql.DB with PostgreSQL placeholder rewriting (?n → $n), so the
// instance stores can keep one statement text per dialect pair.
type DB struct {
	db     *sql.DB
	schema string
}

// OpenDB opens the PostgreSQL server and makes sure the instance schema
// exists. Everything instance-level lives there, next to the per-project
// ledger schemas. Statements address tables as instance.<name>, so callers
// needing isolation (tests) pass their own schema name.
func OpenDB(dsn string) (*DB, error) { return OpenDBSchema(dsn, "instance") }

func OpenDBSchema(dsn, schema string) (*DB, error) {
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	_, err = admin.Exec(`CREATE SCHEMA IF NOT EXISTS ` + quoteIdent(schema))
	_ = admin.Close()
	if err != nil {
		return nil, fmt.Errorf("instance schema: %w", err)
	}
	// The search_path rides on the DSN like the ledger store does, so the
	// unqualified table names in the shared statement text resolve for every
	// pooled connection; public stays on the path for pg_trgm.
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+schema+",public")
	if err != nil {
		return nil, err
	}
	return &DB{db: db, schema: schema}, nil
}

// Schema is the schema this handle addresses tables in.
func (d *DB) Schema() string { return d.schema }

func (d *DB) rewrite(q string) string {
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

func (d *DB) Exec(q string, args ...any) (sql.Result, error) {
	return d.db.Exec(d.rewrite(q), args...)
}

func (d *DB) QueryRow(q string, args ...any) *sql.Row {
	return d.db.QueryRow(d.rewrite(q), args...)
}

func (d *DB) Close() error { return d.db.Close() }

// KV is one JSON document store. File mode keeps the exact file names the
// desktop form uses, so a project directory or data directory made in either
// form reads in the other.
type KV struct {
	db  *DB    // PostgreSQL mode
	dir string // file mode
	tmp string // unused; keeps the struct symmetric in both modes
}

// OpenKVPG opens the instance_state table on the shared server.
func OpenKVPG(dsn string) (*KV, error) { return OpenKVPGSchema(dsn, "instance") }

func OpenKVPGSchema(dsn, schema string) (*KV, error) {
	db, err := OpenDBSchema(dsn, schema)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS ` + db.schema + `.instance_state (
			key     TEXT PRIMARY KEY,
			payload BYTEA NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT extract(epoch FROM now())
		)`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("instance_state schema: %w", err)
		}
	}
	return &KV{db: db}, nil
}

// OpenKVFile keeps documents as files under dir, with the atomic write the
// desktop form has always used.
func OpenKVFile(dir string) (*KV, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &KV{dir: dir}, nil
}

// Get returns the document bytes. The second result is false when the
// document does not exist yet.
func (k *KV) Get(key string) ([]byte, bool, error) {
	if k.db != nil {
		var raw []byte
		err := k.db.QueryRow("SELECT payload FROM "+k.db.schema+".instance_state WHERE key = ?", key).Scan(&raw)
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return raw, true, nil
	}
	raw, err := os.ReadFile(filepath.Join(k.dir, key))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// Put replaces the document atomically. File mode keeps tmp+rename with
// owner-only permissions; PostgreSQL mode is a single upsert.
func (k *KV) Put(key string, raw []byte) error {
	if k.db != nil {
		_, err := k.db.Exec(
			"INSERT INTO "+k.db.schema+".instance_state(key,payload) VALUES(?,?) "+
				"ON CONFLICT(key) DO UPDATE SET payload = excluded.payload, updated_at = extract(epoch FROM now())",
			key, raw)
		return err
	}
	tmp := filepath.Join(k.dir, key+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(k.dir, key))
}

// quoteIdent doubles embedded quotes; schema names here are package
// constants or generated test names, never user input.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func (k *KV) Close() error {
	if k.db != nil {
		return k.db.Close()
	}
	return nil
}
