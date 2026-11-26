package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the persisted snapshot.
type State struct {
	V        int                       `json:"v"`
	LastSeen map[string]int64          `json:"lastSeen"`
	LastID   map[string]string         `json:"lastId,omitempty"`
	KV       map[string]map[string]any `json:"kv"`
}

// Store keeps lastSeen timestamps and per-script key/value state, flushing to
// disk with a short debounce.
type Store struct {
	mu    sync.Mutex
	path  string
	state State
	timer *time.Timer
	// debounce is overridden in tests.
	debounce time.Duration
}

// Open loads state.json, tolerating a missing or corrupt file.
func Open(path string) (*Store, error) {
	s := &Store{
		path:     path,
		debounce: 2 * time.Second,
		state: State{
			V:        1,
			LastSeen: map[string]int64{},
			LastID:   map[string]string{},
			KV:       map[string]map[string]any{},
		},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		// Return the empty store too, so callers can keep running.
		return s, err
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		_ = os.Rename(path, path+".bak")
		s.state = State{V: 1, LastSeen: map[string]int64{}, LastID: map[string]string{}, KV: map[string]map[string]any{}}
		return s, fmt.Errorf("state file corrupt, moved to %s.bak: %w", path, err)
	}
	if s.state.LastSeen == nil {
		s.state.LastSeen = map[string]int64{}
	}
	if s.state.LastID == nil {
		s.state.LastID = map[string]string{}
	}
	if s.state.KV == nil {
		s.state.KV = map[string]map[string]any{}
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) LastSeen(topic string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastSeen[topic]
}

// MarkSeen advances the timestamp for a topic (never moves backwards) and
// records the id of the last processed message. The id is used to suppress the
// replay of that message after a restart.
func (s *Store) MarkSeen(topic string, ts int64, id string) {
	s.mu.Lock()
	if ts > s.state.LastSeen[topic] {
		s.state.LastSeen[topic] = ts
		s.markDirtyLocked()
	}
	if id != "" && s.state.LastID[topic] != id {
		s.state.LastID[topic] = id
		s.markDirtyLocked()
	}
	s.mu.Unlock()
}

// LastID returns the id of the last message processed for a topic.
func (s *Store) LastID(topic string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastID[topic]
}

// Since returns the reconnect timestamp for a group of topics: the smallest
// lastSeen minus a small overlap, or 0 when nothing is known.
func (s *Store) Since(topics []string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	var min int64
	for _, t := range topics {
		ts := s.state.LastSeen[t]
		if ts > now {
			// A server clock that is ahead must not poison the cursor.
			ts = now
		}
		if ts <= 0 {
			continue
		}
		if min == 0 || ts < min {
			min = ts
		}
	}
	if min > 5 {
		min -= 5
	}
	return min
}

func (s *Store) KV(topic string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.state.KV[topic]
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (s *Store) SetKV(topic string, kv map[string]any) {
	s.mu.Lock()
	if kv == nil {
		delete(s.state.KV, topic)
	} else {
		s.state.KV[topic] = kv
	}
	s.markDirtyLocked()
	s.mu.Unlock()
}

func (s *Store) markDirtyLocked() {
	if s.timer != nil {
		return
	}
	s.timer = time.AfterFunc(s.debounce, func() {
		s.mu.Lock()
		s.timer = nil
		s.mu.Unlock()
		_ = s.Flush()
	})
}

// Flush writes the current state atomically.
func (s *Store) Flush() error {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Close() error { return s.Flush() }
