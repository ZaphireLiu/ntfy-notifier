package config

import (
	"fmt"
	"strings"
	"time"
)

// QuietHours is a parsed "HH:MM-HH:MM" range. Ranges may cross midnight.
type QuietHours struct {
	start int // minutes since midnight
	end   int
	raw   string
}

// ParseQuietHours parses "23:30-08:00". An empty string means disabled.
func ParseQuietHours(s string) (*QuietHours, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("expected HH:MM-HH:MM, got %q", s)
	}
	start, err := parseClock(parts[0])
	if err != nil {
		return nil, err
	}
	end, err := parseClock(parts[1])
	if err != nil {
		return nil, err
	}
	if start == end {
		return nil, fmt.Errorf("start and end are identical in %q", s)
	}
	return &QuietHours{start: start, end: end, raw: s}, nil
}

func parseClock(s string) (int, error) {
	s = strings.TrimSpace(s)
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return h*60 + m, nil
}

// Contains reports whether t falls inside the quiet range.
func (q *QuietHours) Contains(t time.Time) bool {
	if q == nil {
		return false
	}
	cur := t.Hour()*60 + t.Minute()
	if q.start < q.end {
		return cur >= q.start && cur < q.end
	}
	// Crosses midnight: [start, 24h) or [0, end).
	return cur >= q.start || cur < q.end
}

func (q *QuietHours) String() string {
	if q == nil {
		return ""
	}
	return q.raw
}
