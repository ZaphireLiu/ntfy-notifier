package subscribe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

type fakeState struct {
	mu   sync.Mutex
	seen map[string]int64
}

func (f *fakeState) Since(topics []string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var min int64
	for _, t := range topics {
		if ts := f.seen[t]; ts > 0 && (min == 0 || ts < min) {
			min = ts
		}
	}
	if min > 5 {
		min -= 5
	}
	return min
}

func (f *fakeState) MarkSeen(topic string, ts int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ts > f.seen[topic] {
		f.seen[topic] = ts
	}
}

func TestStreamReceivesMessages(t *testing.T) {
	var gotSince, gotAuth string
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			gotSince = r.URL.Query().Get("since")
			gotAuth = r.Header.Get("Authorization")
		})
		if r.URL.Path != "/sms,agent/json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"id":"a1","time":100,"event":"open","topic":"sms,agent"}`)
		fmt.Fprintln(w, `{"id":"m1","time":101,"event":"message","topic":"sms","message":"hello","priority":4}`)
	}))
	defer srv.Close()

	state := &fakeState{seen: map[string]int64{"sms": 100}}
	msgs := make(chan model.Message, 4)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Token: "tk_test", Mode: "stream"},
		Topics:    []string{"sms", "agent"},
		State:     state,
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
		Watchdog:  2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sub.Run(ctx)
		close(done)
	}()

	select {
	case m := <-msgs:
		if m.Message != "hello" || m.Priority != 4 {
			t.Fatalf("unexpected message: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not stop")
	}

	if gotAuth != "Bearer tk_test" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotSince != "95" {
		t.Fatalf("since = %q, want 95", gotSince)
	}
	if state.seen["sms"] != 101 {
		t.Fatalf("lastSeen = %d", state.seen["sms"])
	}
}

func TestPollModeUsesTimestampOnFirstRun(t *testing.T) {
	var poll, since string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poll = r.URL.Query().Get("poll")
		since = r.URL.Query().Get("since")
		fmt.Fprintln(w, `{"id":"m2","time":200,"event":"message","topic":"agent","message":"ping"}`)
	}))
	defer srv.Close()

	msgs := make(chan model.Message, 2)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Mode: "poll", PollInterval: config.Duration(time.Hour)},
		Topics:    []string{"agent"},
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sub.Run(ctx)
		close(done)
	}()
	select {
	case <-msgs:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	cancel()
	<-done

	if poll != "1" {
		t.Fatalf("poll = %q", poll)
	}
	if since == "" {
		t.Fatal("since is empty")
	}
}
