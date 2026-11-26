package router

import (
	"testing"
	"time"
)

func TestSeedSuppressesOneReplay(t *testing.T) {
	d := NewDeduper(10, time.Minute)
	d.Seed("sms/m1")
	if !d.Seen("sms/m1") {
		t.Fatal("seeded key was not reported as seen")
	}
	if d.Seen("sms/m2") {
		t.Fatal("fresh key reported as seen")
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	d := NewDeduper(10, time.Minute)
	d.Seed("sms/m1")
	d.Seed("sms/m1")
	if got := len(d.order); got != 1 {
		t.Fatalf("order entries = %d, want 1", got)
	}
}
