//internal/state/memory.go

package state

import (
	"context"
	"sync"
	"time"
)

type memoryRecord struct {
	LotID         string
	SpecDocID     string
	SpecURL       string
	SentAt        *time.Time
	LastCheckedAt time.Time
	Verdict       *bool
	Confidence    *float64
	Reason        string
}

type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]*memoryRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]*memoryRecord)}
}

func (m *MemoryStore) IsProcessed(_ context.Context, lotID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[lotID]
	return ok, nil
}

func (m *MemoryStore) MarkChecked(_ context.Context, lotID, specDocID, specURL string, checkedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.data[lotID]
	if !ok {
		rec = &memoryRecord{LotID: lotID}
		m.data[lotID] = rec
	}
	rec.SpecDocID = specDocID
	rec.SpecURL = specURL
	rec.LastCheckedAt = checkedAt
	return nil
}

func (m *MemoryStore) MarkSent(_ context.Context, lotID string, sentAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.data[lotID]
	if !ok {
		rec = &memoryRecord{LotID: lotID}
		m.data[lotID] = rec
	}
	rec.SentAt = &sentAt
	return nil
}

func (m *MemoryStore) SaveVerdict(_ context.Context, lotID string, verdict bool, confidence float64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.data[lotID]
	if !ok {
		rec = &memoryRecord{LotID: lotID}
		m.data[lotID] = rec
	}
	rec.Verdict = &verdict
	rec.Confidence = &confidence
	rec.Reason = reason
	return nil
}

func (m *MemoryStore) Close() error { return nil }

