package state

import (
	"context"
	"time"
)

type Store interface {
	IsProcessed(ctx context.Context, lotID string) (bool, error)
	MarkChecked(ctx context.Context, lotID, specDocID, specURL string, checkedAt time.Time) error
	MarkSent(ctx context.Context, lotID string, sentAt time.Time) error
	SaveVerdict(ctx context.Context, lotID string, verdict bool, confidence float64, reason string) error
	Close() error
}

