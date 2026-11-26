package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// RestoreSnapshot writes one offline ledger's committed rows into this store.
// It is the server form's import path (docs/16 §13 M0 ticket 5): a .twproj
// package carries the ledger as a SQLite snapshot, while the receiving ledger
// is the project's schema in PostgreSQL.
//
// The snapshot is validated exactly like import validation does — ReadSnapshot
// re-checks schema, rows and the content fingerprint before anything is
// written — and the row bodies are copied verbatim, so a restored ledger keeps
// the source's content hash and version history. The target must be an empty
// ledger: a restore never merges into live rows.
//
// The rebuildable tables (fact_search, relations) are repopulated through
// RebuildIndexes after the load, which is the same write path a live ledger
// uses; the vectors cache is intentionally not carried (it is derived data,
// see vectors.go). The in-memory state is reloaded through loadSQLite, so the
// same appendRow/validateMemory checks a fresh open applies stand guard here.
func (s *Store) RestoreSnapshot(path string) error {
	s.mu.Lock()
	err := s.restoreSnapshotLocked(path)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.RebuildIndexes()
}

func (s *Store) restoreSnapshotLocked(path string) error {
	if s.be == nil {
		return fmt.Errorf("ledger is not open")
	}
	// Validate first, write second: a rejected snapshot leaves the empty target
	// exactly as it was.
	if _, err := ReadSnapshot(path); err != nil {
		return fmt.Errorf("账本快照不可用：%w", err)
	}
	var committed int
	if err := s.be.QueryRow("SELECT count(*) FROM objects").Scan(&committed); err != nil {
		return err
	}
	if committed != 0 {
		return fmt.Errorf("目标账本已包含 %d 行对象；恢复只能写入空账本", committed)
	}
	src, err := sql.Open("sqlite", sqliteURL(path, true))
	if err != nil {
		return err
	}
	defer src.Close()
	src.SetMaxOpenConns(1)
	sb := &dbBackend{db: src}

	var rawMeta []byte
	if err := sb.QueryRow("SELECT payload FROM meta WHERE id=1").Scan(&rawMeta); err != nil {
		return fmt.Errorf("读取快照 meta：%w", err)
	}
	type objRow struct {
		kind                   string
		id                     string
		position, version, seq uint64
		payload                []byte
	}
	rows, err := sb.Query("SELECT kind,id,position,version,seq,payload FROM objects ORDER BY rowid")
	if err != nil {
		return fmt.Errorf("读取快照对象：%w", err)
	}
	snapRows := []objRow{}
	for rows.Next() {
		var r objRow
		var position, version, seq int64
		if err := rows.Scan(&r.kind, &r.id, &position, &version, &seq, &r.payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取快照对象：%w", err)
		}
		r.position, r.version, r.seq = uint64(position), uint64(version), uint64(seq)
		snapRows = append(snapRows, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("读取快照对象：%w", err)
	}
	rows.Close()
	var versionRows [][]byte
	rows, err = sb.Query("SELECT payload FROM versions ORDER BY seq")
	if err != nil {
		return fmt.Errorf("读取快照版本历史：%w", err)
	}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取快照版本历史：%w", err)
		}
		versionRows = append(versionRows, payload)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("读取快照版本历史：%w", err)
	}
	rows.Close()

	tx, err := s.be.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Column names keep the statements valid on both dialects, as in
	// writeSQLiteRow: PostgreSQL's meta/objects carry explicit columns. A fresh
	// open seeds the meta row, so it is replaced, not inserted.
	if _, err := tx.Exec("INSERT INTO meta(id,payload) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", rawMeta); err != nil {
		return fmt.Errorf("写入 meta：%w", err)
	}
	for _, r := range snapRows {
		if _, err := tx.Exec(`INSERT INTO objects(kind,id,position,version,seq,payload) VALUES(?,?,?,?,?,?)`,
			r.kind, r.id, r.position, r.version, r.seq, r.payload); err != nil {
			return fmt.Errorf("写入对象 %s/%s：%w", r.kind, r.id, err)
		}
	}
	for _, payload := range versionRows {
		// The seq/kind/id/version columns live inside the Change JSON; unpack
		// them from the row the snapshot already validated instead of
		// re-deriving them here.
		var c Change
		if err := json.Unmarshal(payload, &c); err != nil {
			return fmt.Errorf("写入版本历史：%w", err)
		}
		if _, err := tx.Exec("INSERT INTO versions(seq,kind,id,version,payload) VALUES(?,?,?,?,?)",
			c.Seq, c.Kind, c.ID, c.Version, payload); err != nil {
			return fmt.Errorf("写入版本历史 seq=%d：%w", c.Seq, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交恢复：%w", err)
	}
	// Reload through the same path a fresh open uses: appendRow rebuilds the
	// typed view and validateMemory re-checks the fingerprint against the
	// copied row bytes. A mismatch here means the copy diverged, not that the
	// snapshot was bad — it passed ReadSnapshot above.
	if err := s.loadSQLite(); err != nil {
		return fmt.Errorf("恢复后重载账本：%w", err)
	}
	return nil
}
