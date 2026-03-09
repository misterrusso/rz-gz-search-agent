package goszakup

import (
	"context"
	"time"

	"rz_gz_search_agent/internal/model"
)

type Client interface {
	SearchLots(ctx context.Context, keywords []string, from time.Time, to time.Time, limit int) ([]model.Lot, error)
	GetLotDocuments(ctx context.Context, lotID string) ([]model.DocumentRef, error)
	DownloadDocument(ctx context.Context, doc model.DocumentRef) ([]byte, string, error)
}

