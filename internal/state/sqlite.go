package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"rz_gz_search_agent/internal/storage"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := storage.EnsureParentDir(path); err != nil {
		return nil, fmt.Errorf("ensure sqlite dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	const q = `
CREATE TABLE IF NOT EXISTS lot_state (
	lot_id TEXT PRIMARY KEY,
	spec_doc_id TEXT,
	spec_url TEXT,
	sent_at DATETIME,
	last_checked_at DATETIME,
	verdict INTEGER,
	confidence REAL,
	reason TEXT
);
CREATE INDEX IF NOT EXISTS idx_lot_state_last_checked ON lot_state(last_checked_at);
`
	_, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("sqlite migrate: %w", err)
	}
	return nil
}

func (s *SQLiteStore) IsProcessed(ctx context.Context, lotID string) (bool, error) {
	const q = `SELECT 1 FROM lot_state WHERE lot_id = ? LIMIT 1`
	var one int
	err := s.db.QueryRowContext(ctx, q, lotID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite is processed: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) MarkChecked(ctx context.Context, lotID, specDocID, specURL string, checkedAt time.Time) error {
	const q = `
INSERT INTO lot_state(lot_id, spec_doc_id, spec_url, last_checked_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(lot_id) DO UPDATE SET
	spec_doc_id = excluded.spec_doc_id,
	spec_url = excluded.spec_url,
	last_checked_at = excluded.last_checked_at
`
	_, err := s.db.ExecContext(ctx, q, lotID, specDocID, specURL, checkedAt.UTC())
	if err != nil {
		return fmt.Errorf("sqlite mark checked: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkSent(ctx context.Context, lotID string, sentAt time.Time) error {
	const q = `
INSERT INTO lot_state(lot_id, sent_at)
VALUES(?, ?)
ON CONFLICT(lot_id) DO UPDATE SET
	sent_at = excluded.sent_at
`
	_, err := s.db.ExecContext(ctx, q, lotID, sentAt.UTC())
	if err != nil {
		return fmt.Errorf("sqlite mark sent: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveVerdict(ctx context.Context, lotID string, verdict bool, confidence float64, reason string) error {
	iv := 0
	if verdict {
		iv = 1
	}
	const q = `
INSERT INTO lot_state(lot_id, verdict, confidence, reason)
VALUES(?, ?, ?, ?)
ON CONFLICT(lot_id) DO UPDATE SET
	verdict = excluded.verdict,
	confidence = excluded.confidence,
	reason = excluded.reason
`
	_, err := s.db.ExecContext(ctx, q, lotID, iv, confidence, reason)
	if err != nil {
		return fmt.Errorf("sqlite save verdict: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

