package telegram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// MessageStore tracks the most recent message the bot posted to each
// (chat, kind) pair, so a follow-up Post with the same kind can delete the
// prior message first and keep the chat tidy. "kind" is caller-supplied
// (e.g. "binding:zone451.example.com") — identical kinds collapse into one
// live message per chat; different kinds coexist.
//
// The store is concurrency-safe; callers don't need to hold a lock. It
// persists to disk so dedup survives service restarts (including the
// deliberate restart after /rotate).
type MessageStore struct {
	path string

	mu     sync.Mutex
	byChat map[int64]map[string]int64 // chat_id → kind → message_id
}

// NewMessageStore loads (or creates) the store at path. Legacy files written
// by earlier versions (flat chat→id map with no kind) are ignored with a
// warning; prior messages stored under the old schema will no longer be
// deleted, but the new ones are tracked correctly going forward.
func NewMessageStore(path string) (*MessageStore, error) {
	if path == "" {
		return nil, fmt.Errorf("telegram message store: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telegram message store: mkdir: %w", err)
	}
	s := &MessageStore{path: path, byChat: map[int64]map[string]int64{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("telegram message store: read: %w", err)
	}

	// Schema v2: {"chat_id": {"kind": message_id, ...}, ...}.
	var v2 map[string]map[string]int64
	if err := json.Unmarshal(data, &v2); err == nil && looksLikeV2(v2) {
		for k, inner := range v2 {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil || inner == nil {
				continue
			}
			cp := make(map[string]int64, len(inner))
			for kind, msgID := range inner {
				cp[kind] = msgID
			}
			s.byChat[id] = cp
		}
		return s, nil
	}

	// Anything else (e.g. legacy flat schema): log and start empty.
	slog.Warn("telegram message store: unknown on-disk schema, discarding", "path", path)
	return s, nil
}

// Get returns the last message_id recorded for (chatID, kind), or 0 if none.
func (s *MessageStore) Get(chatID int64, kind string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inner := s.byChat[chatID]; inner != nil {
		return inner[kind]
	}
	return 0
}

// Set records a new message_id for (chatID, kind) and flushes to disk.
func (s *MessageStore) Set(chatID int64, kind string, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inner := s.byChat[chatID]
	if inner == nil {
		inner = map[string]int64{}
		s.byChat[chatID] = inner
	}
	inner[kind] = messageID
	return s.flushLocked()
}

// Clear removes the record for (chatID, kind) and flushes. If kind is empty,
// every kind for the chat is cleared.
func (s *MessageStore) Clear(chatID int64, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		delete(s.byChat, chatID)
	} else if inner := s.byChat[chatID]; inner != nil {
		delete(inner, kind)
		if len(inner) == 0 {
			delete(s.byChat, chatID)
		}
	}
	return s.flushLocked()
}

func (s *MessageStore) flushLocked() error {
	out := make(map[string]map[string]int64, len(s.byChat))
	for chatID, inner := range s.byChat {
		if len(inner) == 0 {
			continue
		}
		cp := make(map[string]int64, len(inner))
		for k, v := range inner {
			cp[k] = v
		}
		out[strconv.FormatInt(chatID, 10)] = cp
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// looksLikeV2 returns true when the unmarshal yielded a map-of-maps — the
// expected schema. An empty object is considered v2 (fresh install).
func looksLikeV2(v map[string]map[string]int64) bool {
	if len(v) == 0 {
		return true
	}
	for _, inner := range v {
		if inner != nil {
			return true
		}
	}
	return false
}
