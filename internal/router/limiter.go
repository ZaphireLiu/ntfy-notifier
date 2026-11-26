package router

import (
	"sync"
	"time"
)

// Limiter is a token bucket per topic plus one global bucket. A perMin value of
// zero disables limiting for that scope.
type Limiter struct {
	mu      sync.Mutex
	global  *bucket
	globalN int
	topics  map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
	rate   float64 // tokens per second
	burst  float64
	perMin int
}

func newBucket(perMin int, now time.Time) *bucket {
	burst := float64(perMin)
	if burst < 1 {
		burst = 1
	}
	return &bucket{
		tokens: burst,
		last:   now,
		rate:   float64(perMin) / 60.0,
		burst:  burst,
		perMin: perMin,
	}
}

func (b *bucket) allow(now time.Time) bool {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func NewLimiter(globalPerMin int) *Limiter {
	l := &Limiter{globalN: globalPerMin, topics: map[string]*bucket{}}
	if globalPerMin > 0 {
		l.global = newBucket(globalPerMin, time.Now())
	}
	return l
}

// Allow consumes one token for topic. perMin <= 0 means unlimited.
func (l *Limiter) Allow(topic string, perMin int, now time.Time) bool {
	if perMin <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.global != nil && !l.global.allow(now) {
		return false
	}
	b, ok := l.topics[topic]
	if !ok || b.perMin != perMin {
		b = newBucket(perMin, now)
		l.topics[topic] = b
	}
	return b.allow(now)
}
