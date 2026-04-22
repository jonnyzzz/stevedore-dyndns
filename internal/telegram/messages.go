package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// MessageStore tracks the "current binding-URL" message per chat across
// restarts so the bot can delete the prior post before sending a new one,
// keeping the chat tidy.
//
// The store is concurrency-safe; callers don't need to hold a lock.
type MessageStore struct {
	path string

	mu     sync.Mutex
	byChat map[int64]int64 // chat_id → message_id
}

// NewMessageStore loads (or creates) the store at path.
func NewMessageStore(path string) (*MessageStore, error) {
	if path == "" {
		return nil, fmt.Errorf("telegram message store: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telegram message store: mkdir: %w", err)
	}
	s := &MessageStore{path: path, byChat: map[int64]int64{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("telegram message store: read: %w", err)
	}
	// File is a JSON object with string keys (chat IDs) → numeric message IDs.
	var raw map[string]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("telegram message store: decode: %w", err)
	}
	for k, v := range raw {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			continue
		}
		s.byChat[id] = v
	}
	return s, nil
}

// Get returns the last message_id recorded for chatID, or 0 if none.
func (s *MessageStore) Get(chatID int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byChat[chatID]
}

// Set records a new message_id for chatID and flushes to disk.
func (s *MessageStore) Set(chatID, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byChat[chatID] = messageID
	return s.flushLocked()
}

// Clear removes the record for chatID and flushes.
func (s *MessageStore) Clear(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byChat, chatID)
	return s.flushLocked()
}

func (s *MessageStore) flushLocked() error {
	raw := make(map[string]int64, len(s.byChat))
	for k, v := range s.byChat {
		raw[strconv.FormatInt(k, 10)] = v
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
