package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	ok, err := s.IsProcessed(ctx, "lot-1")
	if err != nil {
		t.Fatalf("IsProcessed() error: %v", err)
	}
	if ok {
		t.Fatal("lot should not be processed yet")
	}

	now := time.Now().UTC()
	if err := s.MarkChecked(ctx, "lot-1", "doc-1", "https://example/doc", now); err != nil {
		t.Fatalf("MarkChecked() error: %v", err)
	}
	ok, err = s.IsProcessed(ctx, "lot-1")
	if err != nil {
		t.Fatalf("IsProcessed() error: %v", err)
	}
	if !ok {
		t.Fatal("lot should be marked as processed")
	}

	if err := s.SaveVerdict(ctx, "lot-1", true, 0.81, "relevant"); err != nil {
		t.Fatalf("SaveVerdict() error: %v", err)
	}
	if err := s.MarkSent(ctx, "lot-1", now); err != nil {
		t.Fatalf("MarkSent() error: %v", err)
	}
}

