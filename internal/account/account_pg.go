package account

import (
	"fmt"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/istate"
)

// OpenPG opens the account store on the server. The table lives in the
// instance schema, deliberately separate from every project ledger schema:
// an account is instance state with its own lifecycle, and the ledger refuses
// every format change, so mixing them would couple one to the other. The
// desktop form's account.db does not migrate (docs/16 Q16: existing data is
// test data); the first /setup creates the first account of the new store.
func OpenPG(dsn string) (*Store, error) { return OpenPGSchema(dsn, "instance") }

// OpenPGSchema is OpenPG on a specific schema; tests isolate on it.
func OpenPGSchema(dsn, schema string) (*Store, error) {
	db, err := istate.OpenDBSchema(dsn, schema)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS ` + db.Schema() + `.users (
			id            BIGSERIAL PRIMARY KEY,
			username      TEXT    NOT NULL UNIQUE,
			password_hash TEXT    NOT NULL,
			created_at    BIGINT  NOT NULL,
			updated_at    BIGINT  NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON ` + db.Schema() + `.users(username)`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("account schema: %w", err)
		}
	}
	return &Store{pg: db}, nil
}

// pgCreate inserts one row and returns its id. PostgreSQL has no
// LastInsertId; RETURNING is the equivalent.
func pgCreate(s *Store, username, hash string, now int64) (int64, error) {
	var id int64
	if err := s.pg.QueryRow(
		`INSERT INTO `+s.pg.Schema()+`.users (username, password_hash, created_at, updated_at) VALUES (?,?,?,?) RETURNING id`,
		username, hash, now, now).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// pgUniqueViolation matches PostgreSQL's unique-violation text, the same
// pragmatic check the SQLite form makes on its driver's message.
func pgUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key value violates unique constraint")
}
