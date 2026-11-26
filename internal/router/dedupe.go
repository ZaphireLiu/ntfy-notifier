package router

import (
	"sync"
	"time"
)

// Deduper remembers recently seen message keys. It is a bounded FIFO with a
// TTL, good enough to suppress replays after reconnects.
type Deduper struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]time.Time
	order   []dedupeEntry
}

type dedupeEntry struct {
	key string
	at  time.Time
}

func NewDeduper(max int, ttl time.Duration) *Deduper {
	if max <= 0 {
		max = 1000
	}
	return &Deduper{
		ttl:     ttl,
		max:     max,
		entries: make(map[string]time.Time, max),
	}
}

// Seen reports whether key was seen within the TTL, recording it otherwise.
func (d *Deduper) Seen(key string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	if at, ok := d.entries[key]; ok && now.Sub(at) < d.ttl {
		return true
	}
	d.record(key, now)
	return false
}

// Seed marks a key as already seen without reporting a hit. It is used at
// startup to suppress the replay of the last message processed before a
// restart.
func (d *Deduper) Seed(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.entries[key]; ok {
		return
	}
	d.record(key, time.Now())
}

func (d *Deduper) record(key string, now time.Time) {
	d.entries[key] = now
	d.order = append(d.order, dedupeEntry{key: key, at: now})

	for len(d.order) > d.max {
		old := d.order[0]
		d.order = d.order[1:]
		if at, ok := d.entries[old.key]; ok && at.Equal(old.at) {
			delete(d.entries, old.key)
		}
	}
}
