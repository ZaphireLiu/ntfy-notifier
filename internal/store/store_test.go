package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMarkSeenPersistsLastID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.MarkSeen("sms", 100, "m1")
	s.MarkSeen("sms", 200, "")
	if got := s.LastID("sms"); got != "m1" {
		t.Fatalf("LastID = %q, want m1", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.LastID("sms"); got != "m1" {
		t.Fatalf("LastID after reopen = %q", got)
	}
	if got := reopened.Since([]string{"sms"}); got != 195 {
		t.Fatalf("Since = %d, want 195", got)
	}
}

func TestSinceClampsFutureTimestamps(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.MarkSeen("sms", time.Now().Add(time.Hour).Unix(), "m1")
	if got, now := s.Since([]string{"sms"}), time.Now().Unix(); got > now {
		t.Fatalf("Since = %d, want <= %d", got, now)
	}
}
