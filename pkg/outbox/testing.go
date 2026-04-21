package outbox

import (
	"context"
	"sync"
)

// TestStore is an in-memory MessageInserter for unit testing.
// It captures messages written via InsertTx without requiring Postgres.
// Use with NewPublisher(NewTestStore(), "test-topic") to get a fully
// functional *Publisher that records events for assertion.
type TestStore struct {
	mu       sync.Mutex
	Messages []*Message
}

// NewTestStore creates an in-memory outbox store for testing.
func NewTestStore() *TestStore {
	return &TestStore{}
}

// InsertTx captures the message in memory. The tx argument is ignored — the
// in-memory store has no concept of transactions.
func (s *TestStore) InsertTx(_ context.Context, _ Tx, msg *Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, msg)
	return nil
}

// FindByEventType returns all captured messages matching the given event type.
func (s *TestStore) FindByEventType(eventType string) []*Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*Message
	for _, m := range s.Messages {
		if m.EventType == eventType {
			result = append(result, m)
		}
	}
	return result
}

// Reset clears all captured messages.
func (s *TestStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = nil
}
